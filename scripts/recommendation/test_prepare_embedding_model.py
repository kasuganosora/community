import concurrent.futures
import errno
import hashlib
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import urllib.error
import urllib.request

import prepare_embedding_model as prepare
import serve_embeddings


REPO = "Qdrant/paraphrase-multilingual-MiniLM-L12-v2-onnx-Q"
REVISION = "1" * 40


class ModelInstallTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name) / "target"
        self.root.mkdir(mode=0o750)
        self.inputs = {"model_optimized.onnx": b"fixture", "tokenizer.json": b"{}"}
        self.entries = [
            {"rfilename": name, "size": len(data),
             "blobId": hashlib.sha1(f"blob {len(data)}\0".encode() + data).hexdigest()}
            for name, data in self.inputs.items()
        ]
        opener = mock.Mock()
        opener.open.side_effect = self.response
        patch = mock.patch.object(prepare, "download_opener", return_value=opener)
        patch.start()
        self.addCleanup(patch.stop)

    def response(self, url, *, timeout):
        if "/api/models/" in url:
            value = {"sha": REVISION, "cardData": {"license": "apache-2.0"}, "siblings": self.entries}
            return io.BytesIO(json.dumps(value).encode())
        return io.BytesIO(self.inputs[url.rsplit("/", 1)[1]])

    def install(self):
        return prepare.install_model(REPO, REVISION, self.root)

    def test_retry_in_same_existing_directory(self):
        original = self.root.stat()
        self.inputs["tokenizer.json"] = b"[]"
        with self.assertRaisesRegex(RuntimeError, "Git object hash mismatch"):
            self.install()
        self.assertEqual(list(self.root.iterdir()), [])
        self.inputs["tokenizer.json"] = b"{}"
        self.install()
        manifest = serve_embeddings.load_manifest(self.root)
        self.assertEqual(manifest["revision"], REVISION)
        self.assertEqual((self.root / "model.onnx").read_bytes(), b"fixture")
        self.assertEqual(self.root.stat().st_ino, original.st_ino)
        self.assertEqual(self.root.stat().st_mode, original.st_mode)

    def test_concurrent_install_preserves_completed_directory(self):
        with concurrent.futures.ThreadPoolExecutor(2) as pool:
            jobs = [pool.submit(self.install) for _ in range(2)]
        failures = [job.exception() for job in jobs if job.exception() is not None]
        self.assertEqual(len(failures), 1)
        self.assertIsInstance(failures[0], FileExistsError)
        self.assertEqual(serve_embeddings.load_manifest(self.root)["revision"], REVISION)

    def test_publication_collision_preserves_existing_data(self):
        real_link = os.link

        def concurrent_writer(source, destination):
            if destination.name == "tokenizer.json":
                destination.write_bytes(b"existing user data")
            real_link(source, destination)

        with mock.patch.object(prepare.os, "link", side_effect=concurrent_writer):
            with self.assertRaises(FileExistsError):
                self.install()
        self.assertFalse((self.root / "manifest.json").exists())
        self.assertEqual((self.root / "tokenizer.json").read_bytes(), b"existing user data")

    def test_killed_publisher_can_be_retried(self):
        pid = os.fork()
        if pid == 0:
            real_publish = prepare.publish_artifact

            def stop_after_first(*args):
                real_publish(*args)
                os._exit(31)

            with mock.patch.object(prepare, "publish_artifact", side_effect=stop_after_first):
                self.install()
            os._exit(32)
        _, status = os.waitpid(pid, 0)
        self.assertEqual(os.waitstatus_to_exitcode(status), 31)
        self.assertTrue((self.root / "model.onnx").is_file())
        original_inode = (self.root / "model.onnx").stat().st_ino
        self.assertFalse((self.root / "manifest.json").exists())
        self.install()
        self.assertEqual((self.root / "model.onnx").stat().st_ino, original_inode)
        self.assertEqual(serve_embeddings.load_manifest(self.root)["revision"], REVISION)
        self.assertEqual({p.name for p in self.root.iterdir()}, set(prepare.ARTIFACTS))

    def test_rename_fallback_and_unrelated_staging(self):
        unrelated = self.root / (prepare.STAGING_PREFIX + "user")
        unrelated.mkdir()
        (unrelated / "data").write_text("keep")
        with self.assertRaises(FileExistsError):
            self.install()
        self.assertEqual((unrelated / "data").read_text(), "keep")
        unrelated.rename(self.root.parent / "user")
        with mock.patch.object(prepare.os, "link", side_effect=OSError(errno.EOPNOTSUPP, "no hard links")):
            self.install()
        self.assertEqual(serve_embeddings.load_manifest(self.root)["revision"], REVISION)

    def test_only_https_redirects_are_accepted(self):
        request = urllib.request.Request("https://example.test/model")
        handler = prepare.HTTPSRedirectHandler()
        with self.assertRaisesRegex(urllib.error.URLError, "requires HTTPS"):
            handler.redirect_request(request, None, 302, "Found", {}, "http://example.test/new")
        result = handler.redirect_request(request, None, 302, "Found", {}, "https://cdn.example.test/new")
        self.assertEqual(result.full_url, "https://cdn.example.test/new")


if __name__ == "__main__":
    unittest.main()
