import contextlib
import copy
import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location("export_misskey", Path(__file__).with_name("export_misskey.py"))
m = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(m)


def ident(n):
    return "arvdg3a2" + format(n, "08x")


def fixture():
    a, b, c = (ident(i) for i in (1, 2, 3))
    return {"user": [{"id": a, "username": "alice", "host": None}, {"id": b, "username": "bob", "host": None},
                     {"id": c, "username": "remote", "host": "remote.test", "uri": "https://remote.test/opaque/1"}],
            "user_profile": [{"userId": a, "password": "$2b$test-only-sentinel", "email": "private@example.test", "emailVerified": True}, {"userId": b, "password": None}],
            "user_keypair": [{"userId": a, "publicKey": "TEST_PUBLIC", "privateKey": "TEST_PRIVATE"}, {"userId": b, "publicKey": "TEST_PUBLIC_B", "privateKey": "TEST_PRIVATE_B"}]}


class ExportTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.source = self.root / "source"
        self.source.mkdir()
        self.output = self.root / "output"
        self.output.mkdir(mode=0o700)
        self.tables = fixture()
        self.a, self.b, self.c = (ident(i) for i in (1, 2, 3))

    def tearDown(self):
        self.temp.cleanup()

    def converter(self, tables=None, **kwargs):
        return m.Converter(tables if tables is not None else self.tables, "https://source.test", "aidx", self.source, self.output, **kwargs)

    def test_all_official_id_time_algorithms(self):
        # Independent fixed values derived from published ID layouts, not from converter.
        expected = "2000-01-01T00:00:00.000Z"
        for method, value in (("aid", "0000000000"), ("aidx", "0000000000000000"),
                              ("meid", "80dc6acfac00000000000000"), ("meidg", "g0dc6acfac00" + "0" * 12),
                              ("objectid", "386d43800000000000000000"), ("ulid", "00VHNCZB00" + "0" * 16)):
            with self.subTest(method=method):
                self.assertEqual(m.id_time(value, method), expected)
        with self.assertRaises(ValueError):
            m.id_time("not-an-id", "aidx")

    def test_no_inferred_origin_or_embedded_credentials(self):
        for url in ("", "source.test", "https://name:secret@source.test", "https://source.test/path", "https://source.test/?token=secret"):
            with self.assertRaises(m.ExportError):
                m.origin(url)

    def test_accounts_password_keys_and_raw_secrets_are_private_data(self):
        self.tables["system_account"] = [{"id": ident(9), "userId": self.b, "type": "actor"}]
        b = self.converter().convert()
        self.assertEqual(b["accounts"][0]["actor_uri"], "https://source.test/users/" + self.a)
        self.assertEqual(b["accounts"][2]["actor_uri"], "https://remote.test/opaque/1")
        self.assertTrue(b["accounts"][1]["system"])
        self.assertEqual(len(b["credentials"]), 1)
        self.assertEqual(b["credentials"][0]["password_scheme"], "bcrypt")
        self.assertNotIn("password_salt", b["credentials"][0])
        self.assertEqual(b["credentials"][0]["extensions"], {})
        self.assertTrue(b["keys"][0]["key_id"].endswith("#main-key"))
        self.assertNotIn("TEST_PRIVATE", json.dumps(b["issues"]))
        self.assertNotIn("private@example.test", json.dumps(b["issues"]))
        self.assertTrue(any(r["kind"] == "misskey:user_profile" for r in b["records"]))

    def test_hidden_relationships_survive_profile_export(self):
        self.tables["user_profile"][0].update(followingVisibility="public", followersVisibility="followers")
        b = self.converter().convert()
        self.assertTrue(b["accounts"][0]["hide_collections"])
        self.assertFalse(b["accounts"][1]["hide_collections"])
        self.tables["user_profile"][0]["followersVisibility"] = "public"
        b = self.converter().convert()
        self.assertFalse(b["accounts"][0]["hide_collections"])

    def test_active_mfa_requires_adapter_but_inactive_secret_is_only_raw(self):
        self.tables["user_profile"][0].update(twoFactorEnabled=False, twoFactorSecret="inactive-secret")
        b = self.converter().convert()
        self.assertFalse(b["credentials"][0]["two_factor"])
        self.assertEqual(b["credentials"][0]["extensions"], {})
        self.tables["user_profile"][0]["twoFactorEnabled"] = True
        b = self.converter().convert()
        self.assertTrue(b["credentials"][0]["two_factor"])
        self.assertEqual(b["credentials"][0]["extensions"]["twoFactorSecret"], "inactive-secret")
        self.assertNotIn("inactive-secret", json.dumps(b["issues"]))

    def test_media_bytes_hash_filename_alt_and_variants(self):
        raw = b"unchanged arbitrary file\x00\xff"
        (self.source / "raw").write_bytes(raw)
        (self.source / "preview").write_bytes(b"\x89PNG\r\n\x1a\npreview")
        self.tables["drive_file"] = [{"id": ident(4), "userId": self.a, "storedInternal": True, "accessKey": "raw", "url": "https://source.test/files/raw", "thumbnailAccessKey": "preview", "thumbnailUrl": "https://source.test/files/preview", "webpublicAccessKey": "reserved-not-generated",
                                        "size": len(raw), "name": "original; filename.txt", "type": "text/plain", "comment": "alt-text", "properties": {"width": 2, "height": 3}}]
        b = self.converter().convert()
        self.assertEqual(len(b["media"]), 2)
        self.assertEqual(b["media"][0]["content_type"], "image/png")
        media = b["media"][1]
        self.assertEqual((self.output / media["path"]).read_bytes(), raw)
        self.assertEqual(media["sha256"], m.hashlib.sha256(raw).hexdigest())
        self.assertEqual(media["original_url"], "https://source.test/files/raw")
        self.assertEqual(b["media"][0]["original_url"], "https://source.test/files/preview")
        self.assertEqual(media["description"], "alt-text")
        self.assertEqual(media["name"], "original; filename.txt")
        self.assertEqual((self.output / media["path"]).stat().st_mode & 0o777, 0o600)

    def test_traversal_symlink_fifo_size_and_budget_fail_closed(self):
        (self.root / "secret").write_bytes(b"secret")
        (self.source / "escape").symlink_to(self.root / "secret")
        (self.source / "directory").symlink_to(self.root, target_is_directory=True)
        os.mkfifo(self.source / "fifo")
        c = self.converter()
        for key in ("../secret", "/etc/passwd", "escape", "directory/secret", "fifo"):
            with self.subTest(key=key), self.assertRaises((m.ExportError, OSError)):
                c.copy_asset(key)
        (self.source / "valid").write_bytes(b"bytes")
        with self.assertRaises(m.ExportError):
            c.copy_asset("valid", 6)
        with self.assertRaises(m.ExportError):
            self.converter(max_media_bytes=1).copy_asset("valid")

    def test_missing_local_s3_and_emoji_bytes_are_errors(self):
        self.tables["drive_file"] = [{"id": ident(4), "userId": self.a, "storedInternal": False, "isLink": False, "url": "https://bucket.test/file"}]
        self.tables["emoji"] = [{"id": ident(5), "name": "custom", "host": None, "originalUrl": "http://127.0.0.1/secret"}]
        b = self.converter().convert()
        self.assertTrue(any(i["code"] == "external_storage_required" and i["severity"] == "error" for i in b["issues"]))
        self.assertTrue(any(i["code"] == "emoji_bytes_unavailable" for i in b["issues"]))

    def test_object_storage_reference_keeps_urls_without_copying(self):
        self.tables["drive_file"] = [{"id": ident(4), "userId": self.a, "storedInternal": False, "isLink": False, "accessKey": "files/abc.png", "url": "https://r2.test/files/abc.png", "size": 3}]
        self.tables["emoji"] = [{"id": ident(5), "name": "custom", "host": None, "originalUrl": "https://r2.test/emoji/e.png"}]
        b = self.converter(object_mode="reference", object_base_url="https://r2.test").convert()
        self.assertFalse([i for i in b["issues"] if i["severity"] == "error" and i["kind"] in ("media", "emoji")])
        media = [x for x in b["media"] if x["id"] == ident(4)][0]
        self.assertEqual(media["remote_url"], "https://r2.test/files/abc.png")
        self.assertNotIn("path", media)
        self.assertEqual(b["emojis"][0]["url"], "https://r2.test/emoji/e.png")
        self.assertEqual(b["emojis"][0]["media_id"], "")
        self.assertFalse((self.output / "media").exists())

    def test_object_storage_copy_reads_bucket_mirror(self):
        mirror = self.root / "mirror"
        (mirror / "files").mkdir(parents=True)
        (mirror / "files" / "abc.png").write_bytes(b"abc")
        (mirror / "emoji").mkdir()
        (mirror / "emoji" / "e.png").write_bytes(b"emoji")
        self.tables["drive_file"] = [{"id": ident(4), "userId": self.a, "storedInternal": False, "isLink": False, "accessKey": "files/abc.png", "url": "https://r2.test/files/abc.png", "size": 3}]
        self.tables["emoji"] = [{"id": ident(5), "name": "custom", "host": None, "originalUrl": "https://r2.test/emoji/e.png"}]
        b = self.converter(object_mode="copy", object_dir=mirror, object_base_url="https://r2.test").convert()
        self.assertFalse([i for i in b["issues"] if i["severity"] == "error" and i["kind"] in ("media", "emoji")])
        media = [x for x in b["media"] if x["id"] == ident(4)][0]
        self.assertEqual(media["size"], 3)
        self.assertTrue((self.output / media["path"]).exists())
        self.assertEqual(b["emojis"][0]["media_id"], "emoji:" + ident(5))
        missing = copy.deepcopy(self.tables)
        missing["drive_file"][0]["accessKey"] = "files/missing.png"
        b = self.converter(missing, object_mode="copy", object_dir=mirror, object_base_url="https://r2.test").convert()
        self.assertTrue(any(i["code"] == "media_bytes_unavailable" for i in b["issues"]))

    def test_private_notes_replies_and_chat_pairs_are_operational(self):
        self.tables["note"] = [{"id": ident(4), "userId": self.a, "visibility": "specified", "visibleUserIds": [self.b], "mentions": [self.c], "text": "secret", "fileIds": []},
                                {"id": ident(5), "userId": self.b, "visibility": "specified", "visibleUserIds": [self.a], "replyId": ident(4), "threadId": ident(4)}]
        self.tables["chat_message"] = [{"id": ident(6), "fromUserId": self.a, "toUserId": self.b, "text": "one"},
                                       {"id": ident(7), "fromUserId": self.b, "toUserId": self.a, "text": "two"}]
        self.tables["messaging_message"] = [{"id": ident(8), "userId": self.a, "recipientId": self.b, "text": "old", "createdAt": "2020-01-01T00:00:00Z"}]
        b = self.converter().convert()
        self.assertEqual(b["posts"][0]["recipient_ids"], [self.b])
        self.assertEqual(b["posts"][1]["reply_id"], ident(4))
        self.assertEqual(b["posts"][0]["conversation_id"], b["posts"][1]["conversation_id"])
        self.assertEqual(b["posts"][2]["conversation_id"], b["posts"][3]["conversation_id"])
        self.assertTrue(all(not p.get("archive_only") for p in b["posts"]))
        self.assertTrue(all(p["visibility"] == "direct" for p in b["posts"]))

    def test_room_acl_owner_members_only_and_restricted_roles_archive(self):
        self.tables["chat_room"] = [{"id": ident(5), "ownerId": self.a}]
        self.tables["chat_room_membership"] = [{"id": ident(6), "roomId": ident(5), "userId": self.b}]
        self.tables["chat_message"] = [{"id": ident(7), "fromUserId": self.a, "toRoomId": ident(5)}]
        b = self.converter().convert()
        self.assertEqual(set(b["posts"][0]["recipient_ids"]), {self.a, self.b})
        self.assertFalse(b["posts"][0]["archive_only"])
        self.tables["role"] = [{"id": ident(8), "policies": {"chatAvailability": {"useDefault": False, "value": "unavailable"}}}]
        b = self.converter().convert()
        self.assertTrue(b["posts"][0]["archive_only"])

    def test_polls_votes_lists_bookmarks_reactions_and_expired_mute(self):
        self.tables.update({"poll": [{"noteId": ident(8), "choices": ["yes", "no"], "votes": [3, 4], "multiple": True}],
                           "poll_vote": [{"id": ident(9), "noteId": ident(8), "userId": self.a, "choice": 1}],
                           "note_reaction": [{"id": ident(10), "noteId": ident(8), "userId": self.b, "reaction": ":custom@remote.test:"}],
                           "note_favorite": [{"id": ident(11), "noteId": ident(8), "userId": self.a}],
                           "user_list": [{"id": ident(12), "userId": self.a, "name": "List"}],
                           "user_list_membership": [{"id": ident(13), "userListId": ident(12), "userId": self.b}],
                           "muting": [{"id": ident(14), "muterId": self.a, "muteeId": self.b, "expiresAt": "2000-01-01T00:00:00Z"}]})
        b = self.converter().convert()
        self.assertEqual(b["polls"][0]["options"][1], {"title": "no", "votes": 4})
        self.assertEqual(b["poll_votes"][0]["option"], 1)
        self.assertEqual(b["lists"][0]["member_ids"], [self.b])
        self.assertEqual(len(b["bookmarks"]), 1)
        self.assertEqual(b["reactions"][0]["emoji"], ":custom@remote.test:")
        self.assertEqual(b["relations"][0]["expires_at"], "2000-01-01T00:00:00Z")

    def test_note_emoji_domain_resolution_and_pending_activity(self):
        self.tables["emoji"] = [{"id": ident(10), "name": "local", "host": None}, {"id": ident(11), "name": "remote", "host": "remote.test"}]
        self.tables["user"][0]["emojis"] = ["local"]
        self.tables["user"][2]["emojis"] = ["remote"]
        c = self.converter()
        c.accounts()
        self.assertEqual(c.bundle["accounts"][0]["emoji_ids"], [ident(10)])
        self.assertEqual(c.bundle["accounts"][2]["emoji_ids"], [ident(11)])
        self.assertEqual(c.note_emoji_ids({"id": ident(5), "emojis": ["local", ":local@.:"]}), [ident(10)])
        self.assertEqual(c.note_emoji_ids({"id": ident(6), "userHost": "remote.test", "emojis": ["remote"]}), [ident(11)])
        self.tables["follow_request"] = [{"id": ident(7), "followerId": self.a, "followeeId": self.c, "requestId": "https://remote.test/activity/follow"}]
        c.relationships()
        self.assertEqual(c.bundle["relations"][0]["activity_uri"], "https://remote.test/activity/follow")
        self.tables["following"] = [{"id": ident(8), "followerId": self.a, "followeeId": self.c}, {"id": ident(9), "followerId": self.c, "followeeId": self.a}]
        c = self.converter()
        c.relationships()
        self.assertEqual(c.bundle["relations"][0]["activity_uri"], "https://source.test/follows/" + self.a + "/" + self.c)
        self.assertEqual(c.bundle["relations"][1]["activity_uri"], "")

    def test_psql_snapshot_single_readonly_transaction_and_big_ids(self):
        bindir = self.root / "bin"
        bindir.mkdir()
        psql = bindir / "psql"
        # Fake server process validates the actual SQL fed by our reader.
        psql.write_text("#!/usr/bin/env python3\nimport sys,json\ns=sys.stdin.read()\nassert 'REPEATABLE READ READ ONLY' in s\nassert s.count('BEGIN TRANSACTION')==1\nassert '%I.%I' in s\nassert 'COMMIT;' in s\nprint(json.dumps({'_snapshot':'1:2:'}))\nprint(json.dumps({'table':'user','row':{'id':'9007199254740993','username':'x'}}))\n")
        psql.chmod(0o700)
        with mock.patch.dict(os.environ, {"PATH": str(bindir) + os.pathsep + os.environ["PATH"]}):
            tables, snapshot = m.read_snapshot(self.output)
        self.assertEqual(tables["user"][0]["id"], "9007199254740993")
        self.assertEqual(snapshot, "1:2:")

    def test_atomic_private_bundle_and_nonzero_for_incomplete_assets(self):
        self.tables["drive_file"] = [{"id": ident(4), "userId": self.a, "storedInternal": True, "accessKey": "missing", "size": 7}]
        out = self.root / "final"
        previous_umask = os.umask(0o022)
        try:
            with mock.patch.object(m, "read_snapshot", return_value=(self.tables, "1:2:")), contextlib.redirect_stdout(io.StringIO()) as stdout:
                result = m.main(["--base-url", "https://source.test", "--id-method", "aidx", "--source-version", "2026.10.0", "--source-dir", str(self.source), "--output", str(out)])
            self.assertEqual(result, 2)
            self.assertEqual(out.stat().st_mode & 0o777, 0o700)
            self.assertEqual((out / "bundle.json").stat().st_mode & 0o777, 0o600)
            self.assertFalse((out / "bundle.json.partial").exists())
            self.assertNotIn("TEST_PRIVATE", stdout.getvalue())
            self.assertNotIn("$2b$", stdout.getvalue())
        finally:
            os.umask(previous_umask)


if __name__ == "__main__":
    unittest.main()
