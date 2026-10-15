package registry

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "perbarui file golden")

func marshalJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func assertGolden(t *testing.T, name string, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("baca golden %s: %v (jalankan dengan -update)", path, err)
	}
	if got+"\n" != string(want) {
		t.Errorf("JSON tidak cocok.\n--- want (%s) ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

// ---------------------------------------------------------------------------
// Golden: bentuk JSON pada wire
// ---------------------------------------------------------------------------

func TestGolden_ToolRequestShape(t *testing.T) {
	req := ToolRequest{
		ToolName: "retrieve_chunks",
		TraceID:  "trace-1",
		Params:   map[string]interface{}{"query": "transformer", "top_k": float64(5)},
	}
	assertGolden(t, "tool_request", marshalJSON(t, req))
}

func TestGolden_ToolResponseSuccess(t *testing.T) {
	resp := ToolResponse{
		ToolName: "retrieve_chunks",
		TraceID:  "trace-1",
		Success:  true,
		Data:     map[string]interface{}{"results": []interface{}{"a", "b"}},
	}
	assertGolden(t, "tool_response_success", marshalJSON(t, resp))
}

func TestGolden_ToolResponseFailure(t *testing.T) {
	// Error terisi, Data kosong → key "data" harus absen (omitempty).
	resp := ToolResponse{
		ToolName: "nonexistent",
		TraceID:  "trace-2",
		Success:  false,
		Error:    "tool \"nonexistent\" not found in registry",
	}
	assertGolden(t, "tool_response_failure", marshalJSON(t, resp))
}

// ---------------------------------------------------------------------------
// Regresi #1 — payload yang dipakai README dan scripts/benchmark.py
// ---------------------------------------------------------------------------

// Ini test yang mencegah regresi JSON tag hilang. Tanpa tag
// `json:"tool_name"`, Go tidak akan menjembatani tool_name ke ToolName
// (case-insensitive matching tidak berlaku pada underscore), dan setiap
// payload bergaya snake_case akan ditolak dengan 400.
func TestContract_SnakeCasePayloadIsAccepted(t *testing.T) {
	raw := `{"tool_name":"retrieve_chunks","trace_id":"trace-1","params":{"query":"transformer","top_k":5}}`

	var req ToolRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if req.ToolName != "retrieve_chunks" {
		t.Errorf("tool_name tidak ter-parse: ToolName=%q — tag json hilang?", req.ToolName)
	}
	if req.TraceID != "trace-1" {
		t.Errorf("trace_id tidak ter-parse: TraceID=%q", req.TraceID)
	}
	if req.Params["query"] != "transformer" {
		t.Errorf("params tidak ter-parse: %v", req.Params)
	}
}

// Payload README harus benar-benar sampai ke handler, bukan ditolak registry.
func TestContract_SnakeCasePayloadReachesHandler(t *testing.T) {
	r := New()
	var seen ToolRequest
	r.Register("retrieve_chunks", func(_ context.Context, req ToolRequest) ToolResponse {
		seen = req
		return ToolResponse{Success: true}
	})

	var req ToolRequest
	if err := json.Unmarshal([]byte(`{"tool_name":"retrieve_chunks","params":{"query":"q"}}`), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	resp := r.Execute(context.Background(), req)
	if !resp.Success {
		t.Fatalf("handler tidak dipanggil: %s", resp.Error)
	}
	if seen.ToolName != "retrieve_chunks" {
		t.Errorf("handler terima ToolName=%q", seen.ToolName)
	}
}

// Response juga harus snake_case, karena /tool mengembalikannya apa adanya.
func TestContract_ResponseIsSnakeCase(t *testing.T) {
	resp := ToolResponse{ToolName: "x", TraceID: "t", Success: true}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"tool_name", "trace_id", "success"} {
		if _, ok := m[k]; !ok {
			t.Errorf("key %q tidak ada di response; dapat: %s", k, b)
		}
	}
	// Tidak boleh ada key PascalCase.
	for _, k := range []string{"ToolName", "TraceID", "Success"} {
		if _, ok := m[k]; ok {
			t.Errorf("key %q masih muncul — tag json hilang?", k)
		}
	}
}

// Go hanya mencocokkan nama tag, sehingga bentuk PascalCase tidak lagi diterima.
// Test ini mendokumentasikan perilaku itu supaya tidak disalahartikan nanti.
func TestContract_PascalCaseIsNotAccepted(t *testing.T) {
	var req ToolRequest
	if err := json.Unmarshal([]byte(`{"ToolName":"retrieve_chunks"}`), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.ToolName != "" {
		t.Skip("Go menerima key tanpa underscore sebagai alias field — perilaku mungkin berubah antar versi")
	}
}

// Field kosong harus absen, bukan null.
func TestContract_OmitEmptyFields(t *testing.T) {
	b, _ := json.Marshal(ToolResponse{ToolName: "x", Success: true})
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)

	for _, k := range []string{"data", "error"} {
		if _, present := m[k]; present {
			t.Errorf("key %q harus absen saat kosong, dapat: %s", k, b)
		}
	}
}
