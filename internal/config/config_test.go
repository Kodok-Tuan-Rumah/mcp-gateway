package config

import (
	"strings"
	"testing"
	"time"
)

// envMap mengubah map biasa menjadi fungsi lookup untuk LoadFromLookup.
func envMap(pairs map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := pairs[key]
		return v, ok
	}
}

// ═══════════════════════════ Default ═══════════════════════════

// Default() adalah nilai fallback yang dipakai newRouter() dan test, jadi nilainya
// harus benar tanpa menyentuh environment.
func TestDefault_IsUsableWithoutEnvironment(t *testing.T) {
	cfg := Default()

	if cfg.ListenAddress != DefaultListenAddress {
		t.Errorf("ListenAddress: %q", cfg.ListenAddress)
	}
	if cfg.RequestTimeout != DefaultRequestTimeout {
		t.Errorf("RequestTimeout: %v", cfg.RequestTimeout)
	}
	if cfg.StoreLogTimeout != DefaultStoreLogTimeout {
		t.Errorf("StoreLogTimeout: %v", cfg.StoreLogTimeout)
	}
	if cfg.PostgresDSN != DefaultPostgresDSN {
		t.Errorf("PostgresDSN: %q", cfg.PostgresDSN)
	}
	if cfg.GroqAPIKey != "" {
		t.Errorf("GroqAPIKey harus kosong di default, dapat %q", cfg.GroqAPIKey)
	}
}

// Setiap service wajib punya BaseURL, Timeout, dan batas ukuran response yang
// positif. Timeout nol atau batas nol berarti request tanpa protections.
func TestDefault_EveryServiceIsFullyConfigured(t *testing.T) {
	cfg := Default()

	services := map[string]ServiceConfig{
		"Arxiv":     cfg.Arxiv,
		"Evaluator": cfg.Evaluator,
		"RAG":       cfg.RAG,
	}
	for name, svc := range services {
		t.Run(name, func(t *testing.T) {
			if svc.BaseURL == "" {
				t.Error("BaseURL kosong — adapter tidak punya tujuan")
			}
			if svc.Timeout <= 0 {
				t.Errorf("Timeout harus positif, dapat %v", svc.Timeout)
			}
			if svc.MaxResponseBytes <= 0 {
				t.Errorf("MaxResponseBytes harus positif, dapat %d", svc.MaxResponseBytes)
			}
		})
	}
}

// Default() harus mengembalikan struct baru setiap kali dipanggil. Kalau nilainya
// shared, satu pemanggil bisa mengubah konfigurasi pemanggil lain.
func TestDefault_ReturnsIndependentValues(t *testing.T) {
	a := Default()
	a.Arxiv.BaseURL = "http://diubah"
	a.GroqAPIKey = "kunci"

	b := Default()
	if b.Arxiv.BaseURL == "http://diubah" {
		t.Error("Default() mengembalikan nilai yang/shared — perubahan bisa bocor antar pemanggil")
	}
	if b.GroqAPIKey != "" {
		t.Error("GroqAPIKey ikut berubah — konfigurasi shared antar pemanggil")
	}
}

// ═══════════════════════════ LoadFromLookup ═══════════════════════════

// Environment kosong harus menghasilkan persis nilai default.
func TestLoadFromLookup_EmptyEnvironmentYieldsDefaults(t *testing.T) {
	got, err := LoadFromLookup(envMap(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	want := Default()
	if got != want {
		t.Errorf("environment kosong harus menghasilkan Default().\n got: %+v\nwant: %+v", got, want)
	}
}

// Setiap env var harus terbaca oleh LoadFromLookup.
func TestLoadFromLookup_ReadsEveryOverride(t *testing.T) {
	env := map[string]string{
		"GATEWAY_LISTEN_ADDRESS":   ":9999",
		"POSTGRES_DSN":             "host=db port=5432 user=u password=p dbname=d sslmode=disable",
		"GROQ_API_KEY":             "gsk_test",
		"ARXIV_SERVICE_URL":        "http://arxiv:8080",
		"EVAL_SERVICE_URL":         "http://eval:8002",
		"RAG_SERVICE_URL":          "http://rag:8003",
		"GATEWAY_REQUEST_TIMEOUT":  "45s",
		"STORE_LOG_TIMEOUT":        "3s",
		"ARXIV_TIMEOUT":            "30s",
		"EVAL_TIMEOUT":             "15s",
		"RAG_TIMEOUT":              "5s",
		"ARXIV_MAX_RESPONSE_BYTES": "1024",
		"EVAL_MAX_RESPONSE_BYTES":  "2048",
		"RAG_MAX_RESPONSE_BYTES":   "4096",
	}

	cfg, err := LoadFromLookup(envMap(env))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.ListenAddress != ":9999" {
		t.Errorf("ListenAddress: %q", cfg.ListenAddress)
	}
	if cfg.PostgresDSN != env["POSTGRES_DSN"] {
		t.Errorf("PostgresDSN: %q", cfg.PostgresDSN)
	}
	if cfg.GroqAPIKey != "gsk_test" {
		t.Errorf("GroqAPIKey: %q", cfg.GroqAPIKey)
	}
	if cfg.RequestTimeout != 45*time.Second {
		t.Errorf("RequestTimeout: %v", cfg.RequestTimeout)
	}
	if cfg.StoreLogTimeout != 3*time.Second {
		t.Errorf("StoreLogTimeout: %v", cfg.StoreLogTimeout)
	}
	if cfg.Arxiv.BaseURL != "http://arxiv:8080" || cfg.Arxiv.Timeout != 30*time.Second || cfg.Arxiv.MaxResponseBytes != 1024 {
		t.Errorf("Arxiv: %+v", cfg.Arxiv)
	}
	if cfg.Evaluator.BaseURL != "http://eval:8002" || cfg.Evaluator.Timeout != 15*time.Second || cfg.Evaluator.MaxResponseBytes != 2048 {
		t.Errorf("Evaluator: %+v", cfg.Evaluator)
	}
	if cfg.RAG.BaseURL != "http://rag:8003" || cfg.RAG.Timeout != 5*time.Second || cfg.RAG.MaxResponseBytes != 4096 {
		t.Errorf("RAG: %+v", cfg.RAG)
	}
}

// Hanya satu env var yang di-override: sisanya harus tetap default. Ini yang
// sering rusak kalau parsing disalin-tempel dan ada yang lupa fallback.
func TestLoadFromLookup_PartialOverrideKeepsOtherDefaults(t *testing.T) {
	cfg, err := LoadFromLookup(envMap(map[string]string{"ARXIV_TIMEOUT": "7s"}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.Arxiv.Timeout != 7*time.Second {
		t.Errorf("Arxiv.Timeout harus di-override, got %v", cfg.Arxiv.Timeout)
	}
	want := Default()
	if cfg.Evaluator != want.Evaluator {
		t.Errorf("Evaluator berubah padahal tidak di-override: %+v", cfg.Evaluator)
	}
	if cfg.RAG != want.RAG {
		t.Errorf("RAG berubah padahal tidak di-override: %+v", cfg.RAG)
	}
	if cfg.ListenAddress != want.ListenAddress {
		t.Errorf("ListenAddress berubah padahal tidak di-override: %q", cfg.ListenAddress)
	}
}

// Nilai kosong atau whitespace harus diperlakukan sebagai "tidak di-set", bukan
// sebagai nilai kosong yang menimpa default dengan string kosong.
func TestLoadFromLookup_EmptyValuesFallBackToDefault(t *testing.T) {
	cfg, err := LoadFromLookup(envMap(map[string]string{
		"ARXIV_SERVICE_URL": "",
		"EVAL_SERVICE_URL":  "   ",
		"POSTGRES_DSN":      "",
		"RAG_TIMEOUT":       "",
		"EVAL_TIMEOUT":      "  ",
		"ARXIV_TIMEOUT":     "\t",
	}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	want := Default()
	if cfg.Arxiv.BaseURL != want.Arxiv.BaseURL {
		t.Errorf("string kosong harus fallback, dapat %q", cfg.Arxiv.BaseURL)
	}
	if cfg.Evaluator.BaseURL != want.Evaluator.BaseURL {
		t.Errorf("whitespace harus fallback, dapat %q", cfg.Evaluator.BaseURL)
	}
	if cfg.PostgresDSN != want.PostgresDSN {
		t.Errorf("DSN kosong harus fallback, dapat %q", cfg.PostgresDSN)
	}
	if cfg.RAG.Timeout != want.RAG.Timeout {
		t.Errorf("timeout kosong harus fallback, dapat %v", cfg.RAG.Timeout)
	}
	if cfg.Arxiv.Timeout != want.Arxiv.Timeout {
		t.Errorf("timeout whitespace harus fallback, dapat %v", cfg.Arxiv.Timeout)
	}
}

// Nilai sah harus dipangkas spasi di sekeliling.
func TestLoadFromLookup_TrimsWhitespaceAroundValues(t *testing.T) {
	cfg, err := LoadFromLookup(envMap(map[string]string{
		"ARXIV_SERVICE_URL":       "  http://arxiv:8080  ",
		"RAG_TIMEOUT":             " 9s ",
		"EVAL_MAX_RESPONSE_BYTES": " 8192 ",
	}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.Arxiv.BaseURL != "http://arxiv:8080" {
		t.Errorf("BaseURL belum dipangkas: %q", cfg.Arxiv.BaseURL)
	}
	if cfg.RAG.Timeout != 9*time.Second {
		t.Errorf("Timeout belum dipangkas: %v", cfg.RAG.Timeout)
	}
	if cfg.Evaluator.MaxResponseBytes != 8192 {
		t.Errorf("MaxResponseBytes belum dipangkas: %d", cfg.Evaluator.MaxResponseBytes)
	}
}

// ═══════════════ duration & integer tidak valid → gagal cepat ═══════════════

// Konfigurasi salah harus menggagalkan startup dengan pesan yang menyebut
// env varnya, bukan diam-diam memakai nilai yang tidak disengaja.
func TestLoadFromLookup_RejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name  string
		env   map[string]string
		field string
	}{
		{"duration bukan satuan", map[string]string{"GATEWAY_REQUEST_TIMEOUT": "45"}, "GATEWAY_REQUEST_TIMEOUT"},
		{"duration bukan angka", map[string]string{"ARXIV_TIMEOUT": "abc"}, "ARXIV_TIMEOUT"},
		{"duration nol", map[string]string{"EVAL_TIMEOUT": "0s"}, "EVAL_TIMEOUT"},
		{"duration negatif", map[string]string{"RAG_TIMEOUT": "-5s"}, "RAG_TIMEOUT"},
		{"store log timeout negatif", map[string]string{"STORE_LOG_TIMEOUT": "-1s"}, "STORE_LOG_TIMEOUT"},
		{"bytes nol", map[string]string{"ARXIV_MAX_RESPONSE_BYTES": "0"}, "ARXIV_MAX_RESPONSE_BYTES"},
		{"bytes negatif", map[string]string{"EVAL_MAX_RESPONSE_BYTES": "-1"}, "EVAL_MAX_RESPONSE_BYTES"},
		{"bytes bukan angka", map[string]string{"RAG_MAX_RESPONSE_BYTES": "banyak"}, "RAG_MAX_RESPONSE_BYTES"},
		{"bytes pecahan", map[string]string{"RAG_MAX_RESPONSE_BYTES": "1.5"}, "RAG_MAX_RESPONSE_BYTES"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadFromLookup(envMap(tc.env))
			if err == nil {
				t.Fatal("harus gagal, dapat nil")
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("pesan harus menyebut %q, dapat: %v", tc.field, err)
			}
		})
	}
}

// Saat parsing gagal, config yang dikembalikan harus nol — bukan config
// sebagian terisi yang bisa terpakai kalau pemanggilabaikan error.
func TestLoadFromLookup_ErrorReturnsZeroConfig(t *testing.T) {
	cfg, err := LoadFromLookup(envMap(map[string]string{"ARXIV_TIMEOUT": "batal"}))
	if err == nil {
		t.Fatal("harus gagal")
	}
	if cfg != (Config{}) {
		t.Errorf("saat error config harus kosong, dapat %+v", cfg)
	}
}

// ═══════════════════════ Load (baca environment asli) ═══════════════════════

// Load() adalah jalur yang dipakai main(). Ia harus membaca process
// environment sungguhan, bukan map simulasi.
func TestLoad_ReadsRealEnvironment(t *testing.T) {
	t.Setenv("GATEWAY_LISTEN_ADDRESS", ":7777")
	t.Setenv("ARXIV_SERVICE_URL", "http://dari-env:8080")
	t.Setenv("RAG_TIMEOUT", "11s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ListenAddress != ":7777" {
		t.Errorf("ListenAddress: %q", cfg.ListenAddress)
	}
	if cfg.Arxiv.BaseURL != "http://dari-env:8080" {
		t.Errorf("Arxiv.BaseURL: %q", cfg.Arxiv.BaseURL)
	}
	if cfg.RAG.Timeout != 11*time.Second {
		t.Errorf("RAG.Timeout: %v", cfg.RAG.Timeout)
	}
}

// Nilai rusak di environment asli harus menggagalkan startup.
func TestLoad_RejectsInvalidRealEnvironment(t *testing.T) {
	t.Setenv("EVAL_TIMEOUT", "bukan-duration")

	if _, err := Load(); err == nil {
		t.Fatal("harus gagal saat environment tidak valid")
	}
}

// ═══════════════ konsistensi antar default ═══════════════

// Default URL harus dalam bentuk yang bisa dipakai: ada skema, dan tidak ada
// spasi atau slash menggantung di akhir yang menggabungkan path salah.
func TestDefault_ServiceURLsAreWellFormed(t *testing.T) {
	cfg := Default()

	services := map[string]string{
		"Arxiv":     cfg.Arxiv.BaseURL,
		"Evaluator": cfg.Evaluator.BaseURL,
		"RAG":       cfg.RAG.BaseURL,
	}
	for name, url := range services {
		t.Run(name, func(t *testing.T) {
			if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
				t.Errorf("URL tanpa skema: %q", url)
			}
			if strings.HasSuffix(url, "/") {
				t.Errorf("URL berakhiran slash — bisa menghasilkan path ganda: %q", url)
			}
			if strings.ContainsAny(url, " \t\n") {
				t.Errorf("URL mengandung whitespace: %q", url)
			}
		})
	}
}

// Nilai default harus punya batas ukuran response yang masuk akal. Batas yang
// terlalu kecil akan memotong jawaban sah; terlalu besar membiarkan memory habis.
func TestDefault_MaxResponseBytesAreSane(t *testing.T) {
	cfg := Default()
	const (
		tooSmall = 1024     // < 1 KiB
		tooBig   = 64 << 20 // > 64 MiB
	)

	services := map[string]int64{
		"Arxiv":     cfg.Arxiv.MaxResponseBytes,
		"Evaluator": cfg.Evaluator.MaxResponseBytes,
		"RAG":       cfg.RAG.MaxResponseBytes,
	}
	for name, size := range services {
		t.Run(name, func(t *testing.T) {
			if size < tooSmall {
				t.Errorf("batas %d terlalu kecil, akan memotong jawaban sah", size)
			}
			if size > tooBig {
				t.Errorf("batas %d terlalu besar, berisiko exhaust memory", size)
			}
		})
	}
}

// Request timeout harus lebih besar dari timeout per-service, kalau tidak request
// bisa terpotong sebelum adapter menyelesaikan pekerjaannya.
func TestDefault_RequestTimeoutExceedsServiceTimeouts(t *testing.T) {
	cfg := Default()

	services := map[string]time.Duration{
		"Arxiv":     cfg.Arxiv.Timeout,
		"Evaluator": cfg.Evaluator.Timeout,
		"RAG":       cfg.RAG.Timeout,
	}
	for name, svc := range services {
		if cfg.RequestTimeout < svc {
			t.Errorf("RequestTimeout %v lebih kecil dari %s.Timeout %v — request bisa terpotong sebelum selesai",
				cfg.RequestTimeout, name, svc)
		}
	}
}
