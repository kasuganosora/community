import importlib.util
from pathlib import Path
import sys
import tempfile
import unittest

HERE = Path(__file__).parent
sys.path.insert(0, str(HERE))
SPEC = importlib.util.spec_from_file_location("export_mastodon_db", HERE / "export_mastodon_db.py")
m = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(m)

ALICE = {"id": "109000000000000001", "username": "alice", "domain": None, "avatar_file_name": "a.png", "avatar_file_size": 3}
CAROL = {"id": "109000000000000003", "username": "carol", "domain": "remote.example", "uri": "https://remote.example/users/carol"}


def tables(**extra):
    t = {"accounts": [ALICE, CAROL], "users": [], "statuses": [], "media_attachments": [
        {"id": "111000000000000001", "account_id": ALICE["id"], "file_file_name": "p.png", "file_file_size": 3}]}
    t.update(extra)
    return t


class PaperclipTest(unittest.TestCase):
    def test_id_partition_pads_to_nine_digits(self):
        self.assertEqual(m.id_partition(1), "000/000/001")
        self.assertEqual(m.id_partition("109000000000000001"), "109/000/000/000/000/001")

    def test_paperclip_key_and_remote_cache_prefix(self):
        self.assertEqual(m.paperclip_key("accounts", "avatars", "1", "a.png"), "accounts/avatars/000/000/001/original/a.png")
        self.assertEqual(m.paperclip_key("accounts", "avatars", "1", "a.png", remote=True), "cache/accounts/avatars/000/000/001/original/a.png")
        self.assertIsNone(m.paperclip_key("accounts", "avatars", "1", None))

    def test_ts_adds_utc_suffix_once(self):
        self.assertEqual(m.ts("2024-01-02T03:04:05"), "2024-01-02T03:04:05Z")
        self.assertEqual(m.ts("2024-01-02T03:04:05Z"), "2024-01-02T03:04:05Z")
        self.assertIsNone(m.ts(None))


class ConverterTest(unittest.TestCase):
    def test_account_domain_is_hostname_without_port(self):
        with tempfile.TemporaryDirectory() as out:
            c = m.MastodonConverter(tables(), "http://localhost:18083", out, object_mode="reference", object_base_url="https://cdn.example")
            self.assertEqual(c.bundle["source"]["account_domain"], "localhost")
            self.assertEqual(c.bundle["source"]["base_url"], "http://localhost:18083")

    def test_actor_uris_keep_mastodon_paths(self):
        with tempfile.TemporaryDirectory() as out:
            c = m.MastodonConverter(tables(), "https://masto.example", out, object_mode="reference", object_base_url="https://cdn.example")
            self.assertEqual(c.actor(ALICE), "https://masto.example/users/alice")
            self.assertEqual(c.actor(CAROL), CAROL["uri"])
            self.assertEqual(c.status_uri({"id": "5", "account_id": ALICE["id"]}), "https://masto.example/users/alice/statuses/5")

    def test_reference_mode_points_at_bucket_and_copies_nothing(self):
        with tempfile.TemporaryDirectory() as out:
            c = m.MastodonConverter(tables(), "https://masto.example", out, object_mode="reference", object_base_url="https://cdn.example/")
            c.export_media()
            media = {x["id"]: x for x in c.bundle["media"]}
            self.assertTrue(media)
            for x in media.values():
                self.assertTrue(x["storage_reference"])
                self.assertTrue(x["remote_url"].startswith("https://cdn.example/media_attachments/") or x["remote_url"].startswith("https://cdn.example/accounts/"))
            self.assertEqual(list(Path(out).rglob("*.png")), [])

    def test_reference_mode_requires_base_url(self):
        with tempfile.TemporaryDirectory() as out, self.assertRaises(m.ExportError):
            m.MastodonConverter(tables(), "https://masto.example", out, object_mode="reference")

    def test_object_keys_lists_local_and_remote_cache_keys(self):
        with tempfile.TemporaryDirectory() as out:
            carol = dict(CAROL, avatar_file_name="c.png")
            c = m.MastodonConverter(tables(accounts=[ALICE, carol]), "https://masto.example", out, object_mode="reference", object_base_url="https://cdn.example")
            keys = {k for k, _ in c.object_keys()}
            self.assertIn("accounts/avatars/109/000/000/000/000/001/original/a.png", keys)
            self.assertIn("cache/accounts/avatars/109/000/000/000/000/003/original/c.png", keys)
            self.assertIn("media_attachments/files/111/000/000/000/000/001/original/p.png", keys)

    def test_missing_local_bytes_are_an_error(self):
        with tempfile.TemporaryDirectory() as out, tempfile.TemporaryDirectory() as system:
            c = m.MastodonConverter(tables(), "https://masto.example", out, media_dir=system)
            c.export_media()
            self.assertIn("media_bytes_unavailable", {i["code"] for i in c.bundle["issues"]})


if __name__ == "__main__":
    unittest.main()
