package main

import (
	"github.com/malikimayzar/mcp-gateway/internal/api"
	"github.com/malikimayzar/mcp-gateway/internal/orchestrator"
	"github.com/malikimayzar/mcp-gateway/internal/planner"
)

// Nama tool yang terdaftar di registry.
const (
	toolSearchArxiv    = "search_arxiv"
	toolRetrieveChunks = "retrieve_chunks"
	toolEvaluateAnswer = "evaluate_answer"
)

// backendTools adalah tool yang boleh dipakai untuk sebuah backend.
//
// Ini titik penguncian secara struktural: sebuah plan hanya boleh berisi
// tool dari satu daftar, sehingga satu request tidak bisa memakai dua backend.
// Lihat docs/api-contract.md §2.
//
// retrieve_chunks sengaja TIDAK ada di backend manapun pada v1 — adapter-nya
// masih menembak endpoint yang belum ada di rag-research, dan memakainya lewat
// jalur jawaban akan mencampur bukti antar backend.
var backendTools = map[string][]string{
	api.BackendArxiv: {toolSearchArxiv, toolEvaluateAnswer},
}

// toolsForBackend mengembalikan daftar tool yang diizinkan, atau nil kalau
// backend tidak dikenal.
func toolsForBackend(backend string) []string {
	return backendTools[backend]
}

// toolAllowed melapor apakah sebuah tool boleh dipakai oleh backend tertentu.
func toolAllowed(backend, tool string) bool {
	for _, t := range toolsForBackend(backend) {
		if t == tool {
			return true
		}
	}
	return false
}

// planForBackend menyusun plan deterministik: satu langkah menjawab, lalu
// evaluasi.
//
// Plan deterministik ini hanya memakai adapter ArXiv yang menjadi backend
// jawaban aktif. Adapter retrieve_chunks lama tidak menghasilkan jawaban.
func planForBackend(req api.AskRequest) planner.Plan {
	return planner.Plan{
		Query: req.Query,
		Steps: []planner.Step{
			{
				ToolName: toolSearchArxiv,
				Params: map[string]interface{}{
					"query": req.Query,
					"top_k": float64(req.TopK),
				},
			},
			{
				ToolName: toolEvaluateAnswer,
				Params:   map[string]interface{}{},
			},
		},
	}
}

// planFromGroq menerjemahkan plan dari LLM menjadi langkah eksekusi.
// Tool di luar backend terpilih dibuang, parameter query/top_k ditetapkan dari
// request (bukan dipercaya dari LLM), dan evaluasi (jika diminta) ditempatkan
// setelah langkah yang menghasilkan jawaban. Kalau tidak ada langkah jawaban
// valid, hasilnya kosong supaya pemanggil memakai plan deterministik.
func planFromGroq(orcPlan orchestrator.Plan, req api.AskRequest) planner.Plan {
	var answerStep *planner.Step
	evaluate := false

	for _, step := range orcPlan.Steps {
		if !toolAllowed(req.Backend, step.ToolName) {
			continue
		}
		if step.ToolName == toolEvaluateAnswer {
			evaluate = true
			continue
		}
		if answerStep == nil {
			answerStep = &planner.Step{
				ToolName: step.ToolName,
				Params: map[string]interface{}{
					"query": req.Query,
					"top_k": float64(req.TopK),
				},
			}
		}
	}

	if answerStep == nil {
		return planner.Plan{}
	}

	steps := []planner.Step{*answerStep}
	if evaluate {
		steps = append(steps, planner.Step{ToolName: toolEvaluateAnswer, Params: map[string]interface{}{}})
	}
	return planner.Plan{Query: req.Query, Steps: steps}
}
