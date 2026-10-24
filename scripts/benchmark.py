from datetime import datetime
import json
import statistics
import time

import requests

BASE_URL = "http://localhost:8090"
BACKENDS = ("arxiv", "research_rag")
QUERY = "How does retrieval augmented generation work?"
TIMEOUT_SECONDS = 660


def call_ask(backend):
    response = requests.post(
        f"{BASE_URL}/ask",
        json={"backend": backend, "query": QUERY, "top_k": 3},
        timeout=TIMEOUT_SECONDS,
    )
    response.raise_for_status()
    return response.json()


def benchmark_backend(backend, runs=3):
    elapsed_ms = []
    scores = []
    errors = []

    for run in range(1, runs + 1):
        started = time.perf_counter()
        try:
            response = call_ask(backend)
            elapsed_ms.append((time.perf_counter() - started) * 1000)
            evaluation = response.get("evaluation") or {}
            score = evaluation.get("score")
            decision = response.get("decision")
            evaluation_status = response.get("evaluation_status")
            if isinstance(score, (int, float)):
                scores.append(float(score))
            print(
                f"  run {run}: {elapsed_ms[-1]:.1f} ms; "
                f"decision={decision}; evaluation={evaluation_status}"
            )
        except Exception as error:
            errors.append(str(error))
            print(f"  run {run}: ERROR {error}")

    return {
        "runs": len(elapsed_ms),
        "errors": errors,
        "avg_ms": round(statistics.mean(elapsed_ms), 1) if elapsed_ms else None,
        "min_ms": round(min(elapsed_ms), 1) if elapsed_ms else None,
        "max_ms": round(max(elapsed_ms), 1) if elapsed_ms else None,
        "avg_evaluation_score": round(statistics.mean(scores), 4) if scores else None,
    }


def run_benchmark():
    print("MCP Gateway end-to-end benchmark")
    print(f"Time: {datetime.now().isoformat(timespec='seconds')}")
    print(f"Target: {BASE_URL}/ask")

    try:
        health = requests.get(f"{BASE_URL}/health", timeout=5)
        health.raise_for_status()
        print(f"Gateway: {health.json().get('status')}")
    except Exception as error:
        print(f"Gateway unavailable: {error}")
        return

    results = {}
    for backend in BACKENDS:
        print(f"\nBackend: {backend}")
        results[backend] = benchmark_backend(backend)

    output = {
        "timestamp": datetime.now().isoformat(),
        "gateway": BASE_URL,
        "route": "/ask",
        "query": QUERY,
        "results": results,
    }
    output_path = "/tmp/benchmark_results.json"
    with open(output_path, "w", encoding="utf-8") as file:
        json.dump(output, file, indent=2)
    print(f"\nResults saved to {output_path}")


if __name__ == "__main__":
    run_benchmark()
