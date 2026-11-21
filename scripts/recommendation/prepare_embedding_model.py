#!/usr/bin/env python3
"""Download a pinned, verified public ONNX model for local acceptance testing.

Uses normal HTTPS certificate verification and the configured network proxy.
The Hugging Face repository revision must be a full commit hash. LFS objects
are checked against the repository SHA-256; Git blobs against their object ID.
No Hugging Face token, paid inference service, or remote Python code is needed.
"""
import argparse
import errno
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import stat
import ssl
import tempfile
import urllib.error
import urllib.parse
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
HUB_URL = "https://huggingface.co"


class HTTPSRedirectHandler(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        if urllib.parse.urlsplit(newurl).scheme != "https":
            raise urllib.error.URLError("model download redirect requires HTTPS")
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def download_context():
    if os.environ.get("SSL_CERT_FILE") or os.environ.get("SSL_CERT_DIR"):
        return ssl.create_default_context()
    try:
        import certifi
    except ImportError:
        # Standalone use with only the Python standard library.
        return ssl.create_default_context()
    return ssl.create_default_context(cafile=certifi.where())


def download_opener():
    return urllib.request.build_opener(
        HTTPSRedirectHandler(), urllib.request.HTTPSHandler(context=download_context())
    )


def fetch_json(opener, url):
    with opener.open(url, timeout=30) as response:
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
    if root.exists() and not root.is_dir():
        parser.error("output must be a directory")
    try:
        consumed = install_model(args.repo, args.revision, root)
    except FileExistsError as exc:
        parser.error(str(exc))
    print(json.dumps({"model": args.repo, "revision": args.revision, "bytes": consumed}))


ARTIFACTS = ("model.onnx", "tokenizer.json", "manifest.json")
STAGING_PREFIX = ".community-embedding-"
OWNER_FILE = ".installer"


def owner_record(root, staged):
    dest, work = root.stat(), staged.stat()
    return f"community-model-v1 {dest.st_dev}:{dest.st_ino} {work.st_dev}:{work.st_ino}".encode()


def discard_interrupted_install(root, staged):
    # Only recover our own staging directory for this exact output inode.
    # Unknown files and unrelated directories always belong to the caller.
    if not staged.name.startswith(STAGING_PREFIX):
        return
    info = staged.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid():
        return
    owner = staged / OWNER_FILE
    expected = owner_record(root, staged)
    if (not owner.exists() or not stat.S_ISREG(owner.lstat().st_mode)
            or owner.stat().st_size != len(expected) or owner.read_bytes() != expected):
        return
    if any(child.name not in (*ARTIFACTS, OWNER_FILE) or not stat.S_ISREG(child.lstat().st_mode)
           for child in staged.iterdir()):
        return
    shutil.rmtree(staged)


def publish_artifact(staged, root, name):
    source, target = staged / name, root / name
    try:
        os.link(source, target)
    except OSError as exc:
        if exc.errno not in (errno.EPERM, errno.EOPNOTSUPP, errno.ENOSYS, errno.EMLINK, errno.EXDEV):
            raise
        # Linux volumes without hard links can still atomically rename without
        # replacement. Both paths are on the same mount, including bind mounts.
        import ctypes

        libc = ctypes.CDLL(None, use_errno=True)
        if not hasattr(libc, "renameat2"):
            raise
        at_fdcwd, rename_noreplace = -100, 1
        result = libc.renameat2(at_fdcwd, os.fsencode(source), at_fdcwd, os.fsencode(target), rename_noreplace)
        if result != 0:
            error = ctypes.get_errno()
            raise OSError(error, os.strerror(error), target)


def install_model(repo, revision, root):
    root.mkdir(parents=True, exist_ok=True)
    directory_fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY)
    try:
        fcntl.flock(directory_fd, fcntl.LOCK_EX)
        for entry in root.iterdir():
            discard_interrupted_install(root, entry)
        existing = {p.name for p in root.iterdir()}
        if any(name not in ARTIFACTS[:2] or not stat.S_ISREG((root / name).lstat().st_mode) for name in existing):
            raise FileExistsError("output already contains a model or unrelated files")
        with tempfile.TemporaryDirectory(prefix=STAGING_PREFIX, dir=root) as workspace:
            staged = Path(workspace)
            (staged / OWNER_FILE).write_bytes(owner_record(root, staged))
            consumed = download_model(repo, revision, staged)
            # Process termination may leave fully published artifacts but no
            # manifest. Verify them against the freshly authenticated download.
            expected = json.loads((staged / "manifest.json").read_text())["files"]
            for name in existing:
                with (root / name).open("rb") as artifact:
                    if hashlib.file_digest(artifact, "sha256").hexdigest() != expected[name]:
                        raise FileExistsError(f"existing artifact differs: {name}")
            for name in ARTIFACTS:
                if name not in existing:
                    publish_artifact(staged, root, name)
            return consumed
    finally:
        os.close(directory_fd)


def download_model(repo, revision, staged):
    model = MODELS[repo]
    opener = download_opener()
    metadata = fetch_json(opener, f"{HUB_URL}/api/models/{repo}/revision/{revision}?blobs=true")
    if metadata.get("sha") != revision:
        raise RuntimeError("repository revision mismatch")
    license_value = metadata.get("cardData", {}).get("license")
    license_source = {"repo": repo, "revision": revision}
    if license_value is None and model.get("upstream_license"):
        # The reviewed ONNX conversion explicitly identifies its upstream, but
        # omits the license field. Preserve and verify the pinned upstream MIT
        # declaration instead of claiming the conversion declares one.
        license_source = model["upstream_license"]
        if metadata.get("cardData", {}).get("base_model") != license_source["repo"]:
            raise RuntimeError("ONNX upstream differs from reviewed model")
        upstream = fetch_json(opener, f'{HUB_URL}/api/models/{license_source["repo"]}/revision/{license_source["revision"]}')
        if upstream.get("sha") != license_source["revision"]:
            raise RuntimeError("license source revision mismatch")
        license_value = upstream.get("cardData", {}).get("license")
    if license_value != model["license"]:
        raise RuntimeError("license differs from reviewed model")
    files = {item["rfilename"]: item for item in metadata.get("siblings", [])}
    total = sum(files[name].get("size", files[name].get("lfs", {}).get("size", 0)) for name in model["files"])
    if total <= 0 or total > MAX_TOTAL_BYTES:
        raise RuntimeError("unknown or excessive artifact size")
    manifest = {"model": repo, "revision": revision, "family": model["family"],
                "license": license_value, "license_source": license_source,
                "dimensions": model["dimensions"], "files": {}, "sources": {}}
    consumed = 0
    for remote, local in model["files"].items():
        entry = files[remote]
        expected_size = entry.get("size", entry.get("lfs", {}).get("size", 0))
        if not expected_size:
            raise RuntimeError("missing artifact size")
        sha256 = hashlib.sha256()
        git_hash = hashlib.sha1(f"blob {expected_size}\0".encode())
        url = f"{HUB_URL}/{repo}/resolve/{revision}/{remote}"
        received = 0
        with opener.open(url, timeout=60) as response, (staged / local).open("xb") as output:
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
        manifest["files"][local] = sha256.hexdigest()
        manifest["sources"][local] = {"url": url, "bytes": received, "blob_id": entry.get("blobId")}
    (staged / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    return consumed


if __name__ == "__main__":
    def stop(signum, frame):
        raise SystemExit(128 + signum)

    signal.signal(signal.SIGTERM, stop)
    main()
