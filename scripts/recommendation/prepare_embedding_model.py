#!/usr/bin/env python3
"""Download a pinned, verified public ONNX model for local acceptance testing.

Uses normal HTTPS certificate verification and the configured network proxy.
The Hugging Face repository revision must be a full commit hash. LFS objects
are checked against the repository SHA-256; Git blobs against their object ID.
No Hugging Face token, paid inference service, or remote Python code is needed.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import urllib.request

MODELS = {
    "Xenova/multilingual-e5-small": {
        "family": "e5", "dimensions": 384, "license": "mit",
        "upstream_license": {"repo": "intfloat/multilingual-e5-small",
                             "revision": "614241f622f53c4eeff9890bdc4f31cfecc418b3"},
        "files": {"onnx/model_quantized.onnx": "model.onnx", "tokenizer.json": "tokenizer.json"},
    },
    "Qdrant/paraphrase-multilingual-MiniLM-L12-v2-onnx-Q": {
        "family": "minilm", "dimensions": 384, "license": "apache-2.0",
        "files": {"model_optimized.onnx": "model.onnx", "tokenizer.json": "tokenizer.json"},
    },
}
MAX_TOTAL_BYTES = 1024 * 1024 * 1024


def fetch_json(url):
    with urllib.request.urlopen(url, timeout=30) as response:
        return json.loads(response.read(4 * 1024 * 1024))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", choices=MODELS, default="Xenova/multilingual-e5-small")
    parser.add_argument("--revision", required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9a-f]{40}", args.revision):
        parser.error("revision must be a full, reviewed 40-character commit hash")
    root = args.output.resolve()
    if root.exists() and any(root.iterdir()):
        parser.error("output must be empty; existing model files are preserved")
    model = MODELS[args.repo]
    metadata = fetch_json(f"https://huggingface.co/api/models/{args.repo}/revision/{args.revision}?blobs=true")
    if metadata.get("sha") != args.revision:
        raise RuntimeError("repository revision mismatch")
    license_value = metadata.get("cardData", {}).get("license")
    license_source = {"repo": args.repo, "revision": args.revision}
    if license_value is None and model.get("upstream_license"):
        # The reviewed ONNX conversion explicitly identifies its upstream, but
        # omits the license field. Preserve and verify the pinned upstream MIT
        # declaration instead of claiming the conversion declares one.
        license_source = model["upstream_license"]
        if metadata.get("cardData", {}).get("base_model") != license_source["repo"]:
            raise RuntimeError("ONNX upstream differs from reviewed model")
        upstream = fetch_json(f'https://huggingface.co/api/models/{license_source["repo"]}/revision/{license_source["revision"]}')
        if upstream.get("sha") != license_source["revision"]:
            raise RuntimeError("license source revision mismatch")
        license_value = upstream.get("cardData", {}).get("license")
    if license_value != model["license"]:
        raise RuntimeError("license differs from reviewed model")
    files = {item["rfilename"]: item for item in metadata.get("siblings", [])}
    total = sum(files[name].get("size", files[name].get("lfs", {}).get("size", 0)) for name in model["files"])
    if total <= 0 or total > MAX_TOTAL_BYTES:
        raise RuntimeError("unknown or excessive artifact size")
    root.mkdir(parents=True, exist_ok=True)
    manifest = {"model": args.repo, "revision": args.revision, "family": model["family"],
                "license": license_value, "license_source": license_source,
                "dimensions": model["dimensions"], "files": {}, "sources": {}}
    consumed = 0
    for remote, local in model["files"].items():
        entry = files[remote]
        expected_size = entry.get("size", entry.get("lfs", {}).get("size", 0))
        if not expected_size:
            raise RuntimeError("missing artifact size")
        destination = root / local
        temporary = destination.with_suffix(".partial")
        sha256 = hashlib.sha256()
        git_hash = hashlib.sha1(f"blob {expected_size}\0".encode())
        url = f"https://huggingface.co/{args.repo}/resolve/{args.revision}/{remote}"
        received = 0
        try:
            with urllib.request.urlopen(url, timeout=60) as response, temporary.open("wb") as output:
                while chunk := response.read(1024 * 1024):
                    consumed += len(chunk)
                    received += len(chunk)
                    if consumed > MAX_TOTAL_BYTES or received > expected_size:
                        raise RuntimeError("artifact exceeded declared size")
                    output.write(chunk)
                    sha256.update(chunk)
                    git_hash.update(chunk)
            if received != expected_size:
                raise RuntimeError("artifact size mismatch")
            if entry.get("lfs"):
                expected = entry["lfs"].get("sha256", entry["lfs"].get("oid"))
                if sha256.hexdigest() != expected:
                    raise RuntimeError("LFS SHA-256 mismatch")
            elif git_hash.hexdigest() != entry.get("blobId"):
                raise RuntimeError("Git object hash mismatch")
            os.replace(temporary, destination)
        finally:
            temporary.unlink(missing_ok=True)
        manifest["files"][local] = sha256.hexdigest()
        manifest["sources"][local] = {"url": url, "bytes": received, "blob_id": entry.get("blobId")}
    (root / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(json.dumps({"model": args.repo, "revision": args.revision, "bytes": consumed}))


if __name__ == "__main__":
    main()
