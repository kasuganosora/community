#!/usr/bin/env python3
"""Exercise a running real model with fixed synthetic multilingual inputs."""
import argparse
import json
import math
from pathlib import Path
import time
import urllib.error
import urllib.request

TEXTS = [
    "如何照顾刚领养的小猫？",
    "How do I take care of a newly adopted kitten?",
    "引き取ったばかりの子猫はどうやって世話しますか？",
    "passage: 新领养的小猫需要每天喂猫粮，提供干净饮用水，清理猫砂盆并定期看兽医。",
    "passage: A newly adopted kitten needs cat food, fresh drinking water, a clean litter box, and veterinary care.",
    "passage: 引き取った子猫にはキャットフードと新鮮な水を与え、トイレを清潔にし、獣医の診察を受けさせましょう。",
    "passage: PostgreSQL database indexes accelerate SQL query execution and improve server throughput.",
    "passage: 今週末のサッカー大会では二つのチームが優勝を目指して戦います。",
    "passage: 天文学家通过空间望远镜观测遥远星系和黑洞的运动。",
]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=17701)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    origin = f"http://127.0.0.1:{args.port}"

    def call(path, body=None, expect=200):
        data = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(origin + path, data=data, headers={"Content-Type": "application/json"})
        began = time.perf_counter()
        try:
            with urllib.request.urlopen(request, timeout=12) as response:
                code, raw = response.status, response.read()
        except urllib.error.HTTPError as response:
            code, raw = response.code, response.read()
        if code != expect:
            raise AssertionError(f"unexpected status {code}, expected {expect}")
        return json.loads(raw), (time.perf_counter() - began) * 1000

    health, _ = call("/health")
    first, elapsed = call("/embed", {"input": TEXTS})
    second, repeat_elapsed = call("/embed", {"input": TEXTS})
    vectors = [row["embedding"] for row in first["data"]]
    repeated = [row["embedding"] for row in second["data"]]
    assert len(vectors) == len(TEXTS)
    assert all(len(row) == 384 and all(math.isfinite(v) for v in row) for row in vectors)
    assert all(abs(math.sqrt(sum(v*v for v in row)) - 1) < 1e-5 for row in vectors)
    delta = max(abs(a-b) for row, other in zip(vectors, repeated) for a,b in zip(row, other))
    assert delta < 1e-6, f"identical-batch deterministic delta {delta}"
    relevance = []
    for language, query in zip(("zh", "en", "ja"), vectors[:3]):
        scores = [sum(a*b for a,b in zip(query, document)) for document in vectors[3:]]
        best_relevant, best_irrelevant = max(scores[:3]), max(scores[3:])
        assert best_relevant > best_irrelevant + 0.02, f"semantic smoke failed for {language}"
        relevance.append({"query_language": language, "top_document_language": ("zh", "en", "ja")[scores.index(max(scores))],
                          "best_relevant_cosine": best_relevant, "best_irrelevant_cosine": best_irrelevant})
    # REST batch safety checks; invalid requests must not reach the model.
    call("/embed", {"input": ["query"] * 17}, expect=400)
    call("/embed", {"input": "x" * (16 * 1024 + 1)}, expect=400)
    call("/embed", {"input": "x" * (256 * 1024 + 1)}, expect=413)
    metrics, _ = call("/metrics")
    report = {"health": health, "dimensions": 384, "inputs": len(TEXTS), "finite_normalized": True,
              "repeated_batch_max_delta": delta, "first_request_ms": elapsed, "repeat_request_ms": repeat_elapsed,
              "relevance_smoke": relevance, "invalid_batch_and_sizes_rejected": True, "metrics": metrics}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, ensure_ascii=False))


if __name__ == "__main__":
    main()
