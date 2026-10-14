#!/usr/bin/env python3
"""Loopback-only, bounded CPU ONNX REST embedder for reproducible local QA.

The model directory must contain manifest.json with pinned source revision and
SHA-256 hashes for model.onnx and tokenizer.json. No model is downloaded here.
E5 documents use the explicit `passage: ` document template; other text is a
query. Mean pooling excludes padding; every output is L2 normalized.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import multiprocessing as mp
import os
from pathlib import Path
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MAX_BODY = 256 * 1024
MAX_BATCH = 16
MAX_TEXT_BYTES = 16 * 1024
MAX_TOKENS = 256
INFERENCE_TIMEOUT = 8


def load_manifest(root: Path) -> dict:
    value = json.loads((root / "manifest.json").read_text())
    if not value.get("revision") or value.get("family") not in ("e5", "minilm"):
        raise ValueError("model revision/family is missing")
    for name in ("model.onnx", "tokenizer.json"):
        expected = value.get("files", {}).get(name, "")
        digest = hashlib.file_digest((root / name).open("rb"), "sha256").hexdigest()
        if len(expected) != 64 or digest != expected:
            raise ValueError("model artifact hash mismatch: " + name)
    return value


def model_worker(pipe, root: str, family: str, threads: int):
    # No network client or model auto-loader is involved.
    try:
        import numpy as np
        import onnxruntime as ort
        from tokenizers import Tokenizer
        settings = ort.SessionOptions()
        settings.intra_op_num_threads = threads
        settings.inter_op_num_threads = 1
        settings.log_severity_level = 3
        session = ort.InferenceSession(str(Path(root) / "model.onnx"), settings,
                                       providers=["CPUExecutionProvider"])
        tokenizer = Tokenizer.from_file(str(Path(root) / "tokenizer.json"))
        tokenizer.enable_truncation(max_length=MAX_TOKENS)
        pad_id = tokenizer.token_to_id("<pad>")
        pad_token = "<pad>"
        if pad_id is None:
            pad_id = tokenizer.token_to_id("[PAD]")
            pad_token = "[PAD]"
        if pad_id is None:
            raise ValueError("model padding token is missing")
        tokenizer.enable_padding(pad_id=pad_id, pad_token=pad_token)
        names = {v.name for v in session.get_inputs()}
        pipe.send({"ready": True})
        while True:
            texts = pipe.recv()
            if texts is None:
                break
            if family == "e5":
                texts = [s if s.startswith("passage: ") else "query: " + s for s in texts]
            encoded = tokenizer.encode_batch(texts)
            ids = np.asarray([v.ids for v in encoded], dtype=np.int64)
            mask = np.asarray([v.attention_mask for v in encoded], dtype=np.int64)
            args = {"input_ids": ids, "attention_mask": mask}
            if "token_type_ids" in names:
                args["token_type_ids"] = np.asarray([v.type_ids for v in encoded], dtype=np.int64)
            output = session.run(None, {k: v for k, v in args.items() if k in names})[0]
            if output.ndim != 3:
                raise ValueError("expected token-level ONNX output")
            weights = mask[..., None].astype(np.float32)
            vectors = (output * weights).sum(axis=1) / np.clip(weights.sum(axis=1), 1, None)
            vectors = vectors / np.clip(np.linalg.norm(vectors, axis=1, keepdims=True), 1e-12, None)
            if not np.isfinite(vectors).all():
                raise ValueError("non-finite embedding")
            pipe.send({"vectors": vectors.tolist()})
    except (EOFError, BrokenPipeError):
        pass
    except Exception as exc:
        # Exception messages can contain inputs or paths. Report only the class.
        try:
            pipe.send({"error": type(exc).__name__})
        except (EOFError, BrokenPipeError):
            pass
    finally:
        pipe.close()


class Runtime:
    def __init__(self, root: Path, threads: int):
        self.manifest = load_manifest(root)
        context = mp.get_context("spawn")
        self.pipe, child = context.Pipe()
        self.worker = context.Process(target=model_worker,
                                      args=(child, str(root), self.manifest["family"], threads), daemon=True)
        self.worker.start()
        child.close()
        if not self.pipe.poll(60) or self.pipe.recv() != {"ready": True}:
            self.close()
            raise RuntimeError("model worker did not initialize")
        self.lock = threading.Lock()
        self.metrics_lock = threading.Lock()
        self.stats = {"requests": 0, "inputs": 0, "errors": 0, "inference_ms_total": 0.0,
                      "inference_ms_max": 0.0}
        self.started = time.monotonic()

    def infer(self, texts):
        if not self.lock.acquire(timeout=1):
            raise TimeoutError("queue deadline")
        began = time.monotonic()
        try:
            if not self.worker.is_alive():
                raise RuntimeError("worker unavailable")
            self.pipe.send(texts)
            if not self.pipe.poll(INFERENCE_TIMEOUT):
                self.close()
                raise TimeoutError("inference deadline")
            result = self.pipe.recv()
            vectors = result.get("vectors")
            if not isinstance(vectors, list) or len(vectors) != len(texts):
                raise RuntimeError("worker output invalid")
            dimension = self.manifest["dimensions"]
            if any(len(vector) != dimension for vector in vectors):
                raise RuntimeError("worker dimensions invalid")
            with self.metrics_lock:
                elapsed = (time.monotonic() - began) * 1000
                self.stats["requests"] += 1
                self.stats["inputs"] += len(texts)
                self.stats["inference_ms_total"] += elapsed
                self.stats["inference_ms_max"] = max(self.stats["inference_ms_max"], elapsed)
            return vectors
        except Exception:
            with self.metrics_lock:
                self.stats["errors"] += 1
            raise
        finally:
            self.lock.release()

    def close(self):
        if self.worker.is_alive():
            self.worker.terminate()
            self.worker.join(timeout=0.2)
            if self.worker.is_alive():
                self.worker.kill()
                self.worker.join(timeout=0.2)


class Server(ThreadingHTTPServer):
    daemon_threads = True
    request_queue_size = 8

    def __init__(self, address, runtime):
        self.runtime = runtime
        self.slots = threading.BoundedSemaphore(8)
        super().__init__(address, Handler)

    def process_request(self, request, client_address):
        request.settimeout(5)
        if not self.slots.acquire(blocking=False):
            try:
                request.sendall(b"HTTP/1.1 503 Service Unavailable\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
            finally:
                self.shutdown_request(request)
            return
        try:
            super().process_request(request, client_address)
        except Exception:
            self.slots.release()
            raise

    def process_request_thread(self, request, client_address):
        try:
            super().process_request_thread(request, client_address)
        finally:
            self.slots.release()

    def handle_error(self, request, client_address):
        pass  # Never log request bodies or exception values.


class Handler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        pass

    def reply(self, code, value):
        data = json.dumps(value, separators=(",", ":"), allow_nan=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(data)
        self.close_connection = True

    def do_GET(self):
        runtime = self.server.runtime
        if self.path == "/health":
            good = runtime.worker.is_alive()
            return self.reply(200 if good else 503, {
                "ready": good, "model": runtime.manifest["model"],
                "revision": runtime.manifest["revision"],
                "dimensions": runtime.manifest["dimensions"], "max_tokens": MAX_TOKENS,
            })
        if self.path == "/metrics":
            with runtime.metrics_lock:
                stats = dict(runtime.stats)
            stats["uptime_seconds"] = round(time.monotonic() - runtime.started, 3)
            return self.reply(200, stats)
        return self.reply(404, {"error": "not_found"})

    def do_POST(self):
        if self.path != "/embed":
            return self.reply(404, {"error": "not_found"})
        if self.headers.get("Transfer-Encoding"):
            return self.reply(400, {"error": "content_length_required"})
        try:
            count = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            return self.reply(400, {"error": "invalid_content_length"})
        if not 0 < count <= MAX_BODY:
            return self.reply(413, {"error": "request_too_large"})
        try:
            raw = self.rfile.read(count)
            if len(raw) != count:
                return self.reply(400, {"error": "incomplete_request"})
            value = json.loads(raw)
            texts = value.get("input") if isinstance(value, dict) else None
            if isinstance(texts, str):
                texts = [texts]
            if not isinstance(texts, list) or not 1 <= len(texts) <= MAX_BATCH:
                return self.reply(400, {"error": "invalid_batch"})
            if any(not isinstance(s, str) or not s.strip() or len(s.encode()) > MAX_TEXT_BYTES for s in texts):
                return self.reply(400, {"error": "invalid_text"})
            vectors = self.server.runtime.infer(texts)
        except (ValueError, UnicodeError):
            return self.reply(400, {"error": "invalid_json"})
        except TimeoutError:
            return self.reply(503, {"error": "deadline_exceeded"})
        except Exception:
            return self.reply(503, {"error": "embedder_unavailable"})
        return self.reply(200, {"data": [{"embedding": vector} for vector in vectors]})


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model-dir", type=Path, required=True)
    parser.add_argument("--port", type=int, default=17701)
    parser.add_argument("--threads", type=int, choices=(1, 2, 4), default=2)
    args = parser.parse_args()
    if not 1024 <= args.port <= 65535:
        parser.error("port must be between 1024 and 65535")
    # Tokenizer workers must not consume an unbounded number of CPU threads.
    os.environ["TOKENIZERS_PARALLELISM"] = "false"
    runtime = Runtime(args.model_dir.resolve(), args.threads)
    server = Server(("127.0.0.1", args.port), runtime)
    try:
        server.serve_forever()
    finally:
        server.server_close()
        runtime.close()


if __name__ == "__main__":
    main()
