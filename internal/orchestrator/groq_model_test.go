package orchestrator

import (
	"context"
	"strings"
	"testing"
)

// Test ini menjaga regresi yang ditemukan lewat menjalankan sistemnya:
// GeneratePlan sebelumnya menulis nama model Groq secara hardcode. Groq
// mencabut model dari daftar akses, dan setiap panggilan lalu gagal dengan
// model_not_found — yang tertutup rapi oleh fallback ke plan deterministik.
// /ask tetap mengembalikan jawaban, jadi tidak ada yang tampak rusak.

func TestGeneratePlan_RequiresAPIKey(t *testing.T) {
	_, err := GeneratePlan(context.Background(), "apa itu RAG?", "", DefaultTestModel)
	if err == nil {
		t.Fatal("harus gagal tanpa API key")
	}
	if !strings.Contains(err.Error(), "GROQ_API_KEY") {
		t.Errorf("pesan harus menyebut GROQ_API_KEY, dapat: %v", err)
	}
}

// Model kosong harus ditolak sebelum panggilan jaringan, dan pesannya harus
// menyebutkan GROQ_MODEL — bukan dilewatkan ke provider dan gagal dengan 404.
func TestGeneratePlan_RequiresModel(t *testing.T) {
	_, err := GeneratePlan(context.Background(), "apa itu RAG?", "gsk_test", "")
	if err == nil {
		t.Fatal("harus gagal tanpa model")
	}
	if !strings.Contains(err.Error(), "GROQ_MODEL") {
		t.Errorf("pesan harus menyebut GROQ_MODEL, dapat: %v", err)
	}
}

// Nama model harus diteruskan apa adanya ke permintaan. Ini yang memastikan
// nilai dari config benar-benar dipakai, bukan diabaikan.
func TestGeneratePlan_PassesModelThrough(t *testing.T) {
	// Model fiktif: kalau nama diteruskan, provider akan menolak dengan
	// model_not_found yang menyertakan nama tersebut.
	const fakeModel = "model-yang-tidak-ada-2026"

	_, err := GeneratePlan(context.Background(), "apa itu RAG?", "gsk_test_invalid", fakeModel)
	if err == nil {
		t.Skip("permintaan berhasil — kemungkinannya API key test ternyata valid")
	}
	if !strings.Contains(err.Error(), fakeModel) {
		t.Errorf("pesan harus menyebut model yang dipakai (%s), dapat: %v", fakeModel, err)
	}
}

// DefaultTestModel hanya dipakai test; production mengambilnya dari config.
const DefaultTestModel = "openai/gpt-oss-20b"
