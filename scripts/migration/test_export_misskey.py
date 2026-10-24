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

    def test_misskey_profile_extensions_local_and_remote(self):
        self.tables["user"][0].update({"isCat": True, "updatedAt": "2026-01-02T03:04:05.000Z", "movedToUri": "https://new.test/users/a", "alsoKnownAs": ["https://old.test/users/a"]})
        self.tables["user"][2].update({"isCat": True, "movedToUri": "https://moved.test/u", "alsoKnownAs": ["https://alias.test/u", None]})
        self.tables["user_profile"][0].update({"lang": "ja-JP", "location": "Tokyo", "birthday": "2000-02-29", "followedMessage": "hi",
                                               "fields": [{"name": "web", "value": "https://me.test"}, {"name": "x", "value": "y"}], "verifiedLinks": ["https://me.test"]})
        self.tables["user_profile"].append({"userId": self.c, "location": "Remote City", "birthday": "1999-12-31", "followedMessage": "welcome"})
        self.tables["emoji"] = [{"id": ident(9), "name": "lo", "host": None, "originalUrl": "https://r2.test/e.png", "localOnly": True}]
        b = self.converter(object_mode="reference", object_base_url="https://r2.test").convert()
        acc = {a["id"]: a for a in b["accounts"]}
        a, r, bob = acc[self.a], acc[self.c], acc[self.b]
        self.assertEqual((a["is_cat"], a["language"], a["location"], a["birthday"], a["followed_message"], a["moved_to"], a["also_known_as"]),
                         (True, "ja-JP", "Tokyo", "2000-02-29", "hi", "https://new.test/users/a", ["https://old.test/users/a"]))
        self.assertEqual(a["fields"], [{"name": "web", "value": "https://me.test", "verified_at": "2026-01-02T03:04:05.000Z"}, {"name": "x", "value": "y"}])
        self.assertEqual((r["is_cat"], r["location"], r["birthday"], r["followed_message"], r["moved_to"], r["also_known_as"]),
                         (True, "Remote City", "1999-12-31", "welcome", "https://moved.test/u", ["https://alias.test/u"]))
        for k in ("is_cat", "language", "location", "birthday", "followed_message", "moved_to", "also_known_as"):
            self.assertNotIn(k, bob, "empty values are omitted")
        self.assertTrue(b["emojis"][0]["local_only"])

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

    def test_mixed_id_formats_detected_per_value(self):
        # maid.lat rehearsal: early 10-char aid IDs followed by aidx under config.id=aidx.
        self.assertEqual(m.detect_id_method("8abcdefghi", "aidx")[0], "aid")
        self.assertEqual(m.detect_id_method("9xyzabcd0001abcd", "aidx")[0], "aidx")
        self.assertEqual(m.detect_id_method("g0dc6acfac00" + "0" * 12, "aidx")[0], "meidg")
        self.assertEqual(m.detect_id_method("01ARZ3NDEKTSV4RRFFQ69G5FAV", "aidx")[0], "ulid")
        self.assertEqual(m.detect_id_method("5f1d7a3c0000000000000000", "aidx")[0], "objectid")
        conv = self.converter()
        self.assertTrue(conv.time({"id": "8abcdefghi"}).startswith("20"))
        self.assertFalse([i for i in conv.bundle["issues"] if i["code"] == "invalid_source_time"])
        for bad in ("zzzzzzzzzz", "short", "zzzzzzzzzzzzzzzz"):
            with self.assertRaises(ValueError):
                m.id_time(bad, "aidx")

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
        self.tables["drive_file"] = [{"id": ident(4), "userId": self.a, "storedInternal": False, "isLink": False, "accessKey": "files/abc.png", "url": "https://r2.test/files/abc.png", "size": 3},
                                     {"id": ident(6), "userId": self.a, "storedInternal": False, "isLink": False, "accessKey": "files/def.png", "url": "https://r2.test/files/def.png", "size": 3}]
        self.tables["emoji"] = [{"id": ident(5), "name": "custom", "host": None, "originalUrl": "https://r2.test/emoji/e.png"}]
        b = self.converter(object_mode="reference", object_base_url="https://r2.test").convert()
        # Expected per-file state is summarised once per kind, not raised per file.
        refs = [i for i in b["issues"] if i["code"] == "object_storage_reference"]
        self.assertEqual(sorted((i["kind"], i["source_id"], i["details"]["count"]) for i in refs), [("emoji", "", 1), ("media", "", 2)])
        self.assertFalse([i for i in b["issues"] if i["severity"] == "error" and i["kind"] in ("media", "emoji")])
        media = [x for x in b["media"] if x["id"] == ident(4)][0]
        self.assertEqual(media["remote_url"], "https://r2.test/files/abc.png")
        self.assertNotIn("path", media)
        self.assertTrue(media["storage_reference"])
        self.assertEqual(b["emojis"][0]["url"], "https://r2.test/emoji/e.png")
        self.assertEqual(b["emojis"][0]["media_id"], "")
        self.assertFalse((self.output / "media").exists())

    def test_reference_mode_ignores_a_missing_source_dir(self):
        missing = self.root / "misskey" / "files"
        conv = m.Converter(self.tables, "https://source.test", "aidx", missing, self.output, object_mode="reference", object_base_url="https://r2.test")
        self.assertIsNone(conv.source_dir)
        self.assertIn("accounts", conv.convert())

    def test_a_missing_source_dir_still_fails_outside_reference_mode(self):
        missing = self.root / "misskey" / "files"
        with self.assertRaises(FileNotFoundError):
            m.Converter(self.tables, "https://source.test", "aidx", missing, self.output)

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

    def test_preserved_record_ids_follow_real_primary_keys(self):
        # Round 3: poll has no id column (PK noteId); ids fell back to the
        # author's userId, so two polls of one author collided.
        self.tables.update({"poll": [{"noteId": ident(8), "userId": self.a, "choices": ["a", "b"], "votes": [1, 1], "multiple": True},
                                     {"noteId": ident(9), "userId": self.a, "choices": ["c"], "votes": [0], "multiple": False}],
                            # A multiple-choice vote is one row per choice, each with its own id.
                            "poll_vote": [{"id": ident(10), "noteId": ident(8), "userId": self.b, "choice": 0},
                                          {"id": ident(11), "noteId": ident(8), "userId": self.b, "choice": 1}]})
        b = self.converter().convert()
        ids = {}
        for r in b["records"]:
            ids.setdefault(r["kind"], []).append(r["id"])
        self.assertEqual(sorted(ids["misskey:poll"]), [ident(8), ident(9)])
        self.assertEqual(sorted(ids["misskey:poll_vote"]), [ident(10), ident(11)])
        for kind, values in ids.items():
            self.assertEqual(len(values), len(set(values)), kind)
        self.assertEqual(sorted(v["option"] for v in b["poll_votes"]), [0, 1])

    def test_composite_primary_key_and_duplicate_ids_fail_fast(self):
        self.assertEqual(m.source_record_id("t", {"a": "x", "b": 2}, ["a", "b"]), "x/2")
        self.assertEqual(m.source_record_id("user_profile", {"userId": "u"}, m.KNOWN_PRIMARY_KEYS["user_profile"]), "u")
        self.assertTrue(m.source_record_id("t", {"userId": "u"}, None).startswith("row:"), "never fall back to a foreign key")
        self.tables["poll"] = [{"noteId": ident(8), "userId": self.a}, {"noteId": ident(9), "userId": self.a}]
        staging = m.Staging.from_dict(self.tables)
        staging.set_pkcols("poll", ["userId"])  # a wrong key yields duplicates
        with self.assertRaisesRegex(m.ExportError, "duplicate source ids in table poll"):
            self.converter(staging).convert()

    def test_quote_notes_become_linked_posts_not_archives(self):
        self.tables["note"] = [
            {"id": ident(20), "userId": self.c, "userHost": "remote.test", "visibility": "public", "text": "target", "uri": "https://remote.test/notes/t"},
            {"id": ident(21), "userId": self.a, "visibility": "public", "text": "quote", "renoteId": ident(20)},
            {"id": ident(22), "userId": self.b, "visibility": "public", "renoteId": ident(21)},  # pure renote of the quote
            {"id": ident(23), "userId": self.a, "visibility": "public", "cw": "cw only", "renoteId": ident(21)},
        ]
        posts = {p["id"]: p for p in self.converter().convert()["posts"]}
        q = posts[ident(21)]
        self.assertFalse(q.get("archive_only"))
        self.assertEqual(q["quote_id"], ident(20))
        self.assertEqual(q["reblog_id"], "")
        self.assertEqual(q["quote_uri"], "https://remote.test/notes/t")
        self.assertEqual(posts[ident(22)]["reblog_id"], ident(21), "a pure renote stays a boost")
        self.assertNotIn("quote_id", posts[ident(22)])
        self.assertEqual(posts[ident(23)]["quote_id"], ident(21))
        self.assertEqual(posts[ident(23)]["quote_uri"], "https://source.test/notes/" + ident(21), "local target URI")

    def test_unattached_drive_files_exported_unless_skipped(self):
        self.tables["drive_file"] = [
            {"id": ident(50), "userId": self.a, "name": "used.png", "type": "image/png", "url": "https://h/u", "isLink": True},
            {"id": ident(51), "userId": self.a, "name": "drive.png", "type": "image/png", "url": "https://h/d", "isLink": True},
        ]
        self.tables["note"] = [{"id": ident(52), "userId": self.a, "visibility": "public", "text": "x", "fileIds": [ident(50)]}]
        media = {m["id"]: m for m in self.converter().convert()["media"]}
        self.assertTrue(media[ident(51)]["unattached"])
        self.assertNotIn("unattached", media[ident(50)])
        conv = self.converter()
        conv.skip_unattached = True
        b = conv.convert()
        self.assertEqual([m["id"] for m in b["media"]], [ident(50)])
        self.assertIn("unattached_drive_files_skipped", [i["code"] for i in b["issues"]])

    def test_quote_of_target_outside_export_keeps_quote_id(self):
        self.tables["note"] = [{"id": ident(60), "userId": self.a, "visibility": "public", "text": "q", "renoteId": ident(61), "renoteUserHost": None},
                               {"id": ident(62), "userId": self.a, "visibility": "public", "text": "q2", "renoteId": ident(63), "renoteUserHost": "remote.test"}]
        sink = m.FileSink(self.output, {"schema": m.SCHEMA})
        self.converter(sink=sink).convert()
        sink.finish()
        posts = {p["id"]: p for p in json.loads((self.output / "bundle.json").read_text())["posts"]}
        self.assertEqual(posts[ident(60)]["quote_id"], ident(61), "importer reports it as target_missing")
        self.assertEqual(posts[ident(60)]["quote_uri"], "https://source.test/notes/" + ident(61))
        self.assertEqual(posts[ident(62)]["quote_id"], ident(63))

    def test_ownerless_files_become_site_assets_or_are_listed(self):
        f = lambda n, url: {"id": ident(n), "userId": None, "userHost": None, "name": "f%d.png" % n, "type": "image/png", "url": url, "isLink": True}
        self.tables["drive_file"] = [f(70, "https://h/icon"), f(71, "https://h/emoji"), f(72, "https://h/logo"), f(73, "https://h/ad"), f(74, "https://h/none")]
        self.tables["meta"] = [{"id": "x", "iconUrl": "https://h/icon", "app512IconUrl": "https://h/icon", "logoImageUrl": "https://h/logo"}]
        self.tables["emoji"] = [{"id": ident(75), "name": "e", "host": None, "originalUrl": "https://h/emoji", "publicUrl": "https://h/emoji", "type": "image/png"}]
        self.tables["ad"] = [{"id": ident(76), "imageUrl": "https://h/ad"}]
        b = self.converter().convert()
        media = {m["id"]: m for m in b["media"]}
        self.assertEqual(media[ident(70)]["site_assets"], ["app512IconUrl", "iconUrl"])
        for n in (71, 72, 73, 74):
            self.assertNotIn(ident(n), media)
        listed = {i["source_id"]: i["message"] for i in b["issues"] if i["code"] == "misskey_meta_asset"}
        self.assertEqual(set(listed), {ident(72), ident(73), ident(74)}, "emoji images are migrated as emojis, not listed")
        self.assertIn("meta.logoImageUrl", listed[ident(72)])
        self.assertIn("ad image", listed[ident(73)])
        self.assertIn("not referenced", listed[ident(74)])
        details = {i["source_id"]: i["details"] for i in b["issues"] if i["code"] == "misskey_meta_asset"}
        self.assertEqual(details[ident(72)], {"usage": "meta.logoImageUrl", "file_name": "f72.png", "size": 0, "content_type": "image/png"})

    def decoration_tables(self):
        (self.source / "deco1").write_bytes(b"\x89PNG\r\n\x1a\ndeco")
        self.tables["avatar_decoration"] = [
            {"id": ident(80), "name": "Cat ears", "description": "meow", "url": "https://source.test/files/deco1", "roleIdsThatCanBeUsedThisDecoration": [ident(90)]},
            {"id": ident(81), "name": "Lost", "description": "", "url": "https://elsewhere.test/x.png", "roleIdsThatCanBeUsedThisDecoration": []}]
        # The decoration image is an ownerless drive file: exported with the decoration, not listed.
        self.tables["drive_file"] = [{"id": ident(82), "userId": None, "userHost": None, "name": "deco1", "type": "image/png", "url": "https://source.test/files/deco1", "isLink": True}]
        self.tables["user"][0]["avatarDecorations"] = [{"id": ident(80), "angle": 0.125, "flipH": True, "offsetX": 0.05, "offsetY": -0.1}, {"id": "unknownid"}]
        self.tables["user"][1]["avatarDecorations"] = []
        self.tables["user"][2]["avatarDecorations"] = [{"id": ident(80)}]  # remote user: not migrated

    def test_avatar_decorations_definitions_images_and_selections(self):
        self.decoration_tables()
        b = self.converter().convert()
        defs = {d["id"]: d for d in b["avatar_decorations"]}
        self.assertEqual(defs[ident(80)], {"id": ident(80), "name": "Cat ears", "description": "meow", "url": "https://source.test/files/deco1", "media_id": "avatar_decoration:" + ident(80)})
        self.assertNotIn("media_id", defs[ident(81)])
        self.assertNotIn("roleIdsThatCanBeUsedThisDecoration", defs[ident(80)])
        media = {x["id"]: x for x in b["media"]}
        asset = media["avatar_decoration:" + ident(80)]
        self.assertEqual(asset["owner_id"], "")
        self.assertEqual(asset["content_type"], "image/png")
        self.assertEqual(asset["original_url"], "https://source.test/files/deco1")
        self.assertTrue((self.output / asset["path"]).exists())
        self.assertNotIn(ident(82), media)
        codes = {i["code"]: i for i in b["issues"]}
        self.assertNotIn("misskey_meta_asset", codes, "decoration images are migrated, not listed as skipped")
        self.assertEqual(codes["avatar_decoration_image_unavailable"]["source_id"], ident(81))
        self.assertEqual(codes["avatar_decoration_roles_dropped"]["details"], {"count": 1})
        self.assertEqual(b["avatar_decoration_selections"], [{"account_id": self.a, "decorations": [
            {"id": ident(80), "angle": 0.125, "offset_x": 0.05, "offset_y": -0.1, "flip_h": True}, {"id": "unknownid"}]}])

    def test_avatar_decorations_object_storage_reference(self):
        self.tables["avatar_decoration"] = [{"id": ident(80), "name": "R2", "url": "https://r2.test/deco/a.png"}]
        b = self.converter(object_mode="reference", object_base_url="https://r2.test").convert()
        asset = [x for x in b["media"] if x["id"] == "avatar_decoration:" + ident(80)][0]
        self.assertTrue(asset["storage_reference"])
        self.assertEqual(asset["remote_url"], "https://r2.test/deco/a.png")
        self.assertEqual(b["avatar_decorations"][0]["media_id"], asset["id"])

    def test_avatar_decorations_stream_sink(self):
        self.decoration_tables()
        expected = self.converter().convert()
        out = io.StringIO()
        sink = m.StreamSink(self.output, {k: v for k, v in expected.items() if k not in m.SECTIONS}, stream=out)
        self.converter(sink=sink).convert()
        sink.finish()
        lines = [json.loads(x) for x in out.getvalue().splitlines()[1:]]
        for key in ("avatar_decorations", "avatar_decoration_selections"):
            self.assertEqual([x["v"] for x in lines if x["s"] == key], expected[key], key)
        self.assertEqual(len([x for x in lines if x["s"] == "avatar_decorations"]), 2)

    def test_system_actors_and_relays(self):
        ia, ra = ident(30), ident(31)
        self.tables["user"] += [{"id": ia, "username": "instance.actor", "host": None}, {"id": ra, "username": "relay.actor", "host": None}]
        self.tables["user_keypair"] += [{"userId": ia, "publicKey": "IA_PUB", "privateKey": "IA_PRIV"}, {"userId": ra, "publicKey": "RA_PUB", "privateKey": "RA_PRIV"}]
        self.tables["relay"] = [{"id": ident(40), "inbox": "https://relay.example/inbox", "status": "accepted"}]
        b = self.converter().convert()
        accounts = {a["id"]: a for a in b["accounts"]}
        self.assertEqual(accounts[ia]["system_role"], "instance_actor")
        self.assertEqual(accounts[ia]["actor_uri"], "https://source.test/users/" + ia)
        self.assertEqual(accounts[ra]["system_role"], "relay_actor")
        self.assertTrue(accounts[ia]["system"])
        keys = {k["account_id"]: k for k in b["keys"] if k.get("private_key")}
        self.assertEqual(keys[ia]["key_id"], "https://source.test/users/" + ia + "#main-key")
        self.assertEqual(b["relays"], [{"id": ident(40), "inbox": "https://relay.example/inbox", "state": "accepted", "actor_id": ra,
                                        "actor_uri": "https://source.test/users/" + ra, "follow_activity_id": "https://source.test/activities/follow-relay/" + ident(40)}])

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
        c = self.converter()
        c.relationships()
        self.assertEqual(c.bundle["relations"][0]["activity_uri"], "https://remote.test/activity/follow")
        self.tables["following"] = [{"id": ident(8), "followerId": self.a, "followeeId": self.c}, {"id": ident(9), "followerId": self.c, "followeeId": self.a}]
        c = self.converter()
        c.relationships()
        self.assertEqual(c.bundle["relations"][0]["activity_uri"], "https://source.test/follows/" + self.a + "/" + self.c)
        self.assertEqual(c.bundle["relations"][1]["activity_uri"], "")

    def fake_psql(self, body):
        bindir = self.root / "bin"
        bindir.mkdir(exist_ok=True)
        psql = bindir / "psql"
        psql.write_text("#!/usr/bin/env python3\nimport sys, os, json\n" + body)
        psql.chmod(0o700)
        return mock.patch.dict(os.environ, {"PATH": str(bindir) + os.pathsep + os.environ["PATH"]})

    def test_psql_pages_are_short_readonly_keyset_transactions(self):
        log = self.root / "calls.jsonl"
        body = ("s = sys.stdin.read()\nopen(%r, 'a').write(json.dumps({'sql': s, 'argv': sys.argv, 'opts': os.environ.get('PGOPTIONS', '')}) + '\\n')\n"
                "assert s.startswith('BEGIN TRANSACTION ISOLATION LEVEL READ COMMITTED READ ONLY;') and s.rstrip().endswith('COMMIT;')\n"
                "if 'pg_class' in s:\n    print(json.dumps({'t': 'user', 'cols': ['id', 'host'], 'pk': 'id', 'est': 3}))\n"
                "elif 'count(*)' in s:\n    print(3)\n"
                "elif \"> :'last'\" not in s:\n    print(json.dumps({'id': '9007199254740993', 'host': None})); print(json.dumps({'id': '9007199254740994', 'host': None}))\n"
                "else:\n    assert any(a == 'last=9007199254740994' for a in sys.argv), sys.argv\n    print(json.dumps({'id': '9007199254740995', 'host': None}))\n") % str(log)
        with self.fake_psql(body):
            src = m.PsqlSource(self.output, batch_size=2)
            st = m.Staging(self.output / "state.sqlite3")
            f = m.Fetcher(src, st)
            f.scan("user", "t.host IS NULL")
        calls = [json.loads(l) for l in log.read_text().splitlines()]
        self.assertTrue(all("default_transaction_read_only=on" in c["opts"] for c in calls))
        pages = [c for c in calls if "LIMIT 2" in c["sql"]]
        self.assertEqual(len(pages), 2)
        self.assertIn('ORDER BY t."id" LIMIT 2', pages[0]["sql"])
        self.assertEqual(st.count("user"), 3)
        self.assertEqual(st.get("user", "9007199254740993")["id"], "9007199254740993")
        self.assertEqual(st.task("scan:user"), ("9007199254740995", 3, 1))

    def test_resume_continues_from_checkpoint(self):
        st = m.Staging(self.output / "state.sqlite3")
        st.save_task("scan:user", "0000000000000005", 5, 0)
        seen = []
        body = ("s = sys.stdin.read()\n"
                "if 'pg_class' in s:\n    print(json.dumps({'t': 'user', 'cols': ['id'], 'pk': 'id', 'est': 6}))\n"
                "elif 'count(*)' in s:\n    print(6)\n"
                "else:\n    assert 'last=0000000000000005' in sys.argv, sys.argv\n    print(json.dumps({'id': '0000000000000006'}))\n")
        stream = io.StringIO()
        with self.fake_psql(body):
            f = m.Fetcher(m.PsqlSource(self.output, batch_size=10), st, m.Progress(interval=0, stream=stream))
            f.scan("user")
        self.assertIn("done=5/6", stream.getvalue())  # progress starts at the checkpoint, not 0%
        self.assertIn("done=6/6 (100.0%)", stream.getvalue())
        self.assertEqual(st.task("scan:user")[1], 6)

    def test_failure_reports_stage_cause_and_exit_code(self):
        body = "sys.stdin.read()\nsys.stderr.write('WARNING: password file \"/x/.pgpass\" has group or world access\\nfe_sendauth: no password supplied password=hunter2\\n')\nsys.exit(2)\n"
        err = io.StringIO()
        with self.fake_psql(body), contextlib.redirect_stderr(err), contextlib.redirect_stdout(io.StringIO()):
            code = m.main(["--base-url", "https://source.test", "--id-method", "aidx", "--source-version", "x", "--output", str(self.root / "out")])
        self.assertEqual(code, 1)
        text = err.getvalue()
        self.assertIn("Export failed during connecting to source (catalog)", text)
        self.assertIn("password file", text)
        self.assertIn("no password supplied", text)
        self.assertNotIn("hunter2", text)
        self.assertNotIn("Traceback", text)
        err = io.StringIO()
        with self.fake_psql(body), contextlib.redirect_stderr(err), contextlib.redirect_stdout(io.StringIO()):
            m.main(["--base-url", "https://source.test", "--id-method", "aidx", "--source-version", "x", "--output", str(self.root / "out"), "--verbose"])
        self.assertIn("Traceback", err.getvalue())
        self.assertNotIn("hunter2", err.getvalue())

    def test_dsn_with_password_is_refused(self):
        with self.assertRaises(m.ExportError):
            m.PsqlSource(self.output, dsn="host=db password=secret")
        with self.assertRaises(m.ExportError):
            m.PsqlSource(self.output, dsn="postgresql://u:secret@db/misskey")

    def test_also_known_as_simple_array_text_is_split(self):
        # Upstream Misskey: user.alsoKnownAs is TypeORM simple-array (comma-joined text).
        self.tables["user"][0]["alsoKnownAs"] = "https://old.test/users/a, https://older.test/users/a,,https://old.test/users/a"
        self.tables["user"][2]["alsoKnownAs"] = "https://alias.test/u"
        self.tables["user"][1]["alsoKnownAs"] = ""
        acc = {a["id"]: a for a in self.converter().convert()["accounts"]}
        self.assertEqual(acc[self.a]["also_known_as"], ["https://old.test/users/a", "https://older.test/users/a"])
        self.assertEqual(acc[self.c]["also_known_as"], ["https://alias.test/u"])
        self.assertNotIn("also_known_as", acc[self.b])

    def test_only_local_users_pins_are_exported(self):
        self.tables["note"] = [{"id": ident(4), "userId": self.a, "visibility": "public", "text": "mine", "fileIds": []},
                               {"id": ident(5), "userId": self.c, "userHost": "remote.test", "uri": "https://remote.test/n/5", "visibility": "public", "text": "theirs", "fileIds": []}]
        self.tables["user_note_pining"] = [{"id": ident(6), "userId": self.a, "noteId": ident(4)}, {"id": ident(7), "userId": self.c, "noteId": ident(5)}]
        posts = {p["id"]: p for p in self.converter().convert()["posts"]}
        self.assertTrue(posts[ident(4)]["pinned"])
        self.assertFalse(posts[ident(5)]["pinned"], "a remote user's pins are its server's featured collection")

    def test_file_sink_streams_same_bundle_as_memory(self):
        self.tables["note"] = [{"id": ident(4), "userId": self.a, "visibility": "public", "text": "hi", "fileIds": []}]
        self.decoration_tables()
        expected = self.converter().convert()
        sink = m.FileSink(self.output, {k: v for k, v in expected.items() if k not in m.SECTIONS})
        c = self.converter(sink=sink)
        c.convert()
        sink.finish()
        got = json.loads((self.output / "bundle.json").read_text())
        for key in m.SECTIONS:
            self.assertEqual(got[key], expected[key], key)
        self.assertFalse((self.output / ".spool").exists())

    def test_streaming_build_trims_references_outside_export(self):
        self.tables["note"] = [{"id": ident(4), "userId": self.a, "visibility": "public", "replyId": ident(99), "renoteId": ident(98), "fileIds": [ident(97)], "visibleUserIds": [ident(96)]}]
        self.tables["note_reaction"] = [{"id": ident(10), "noteId": ident(95), "userId": self.b, "reaction": "x"}]
        self.tables["following"] = [{"id": ident(11), "followerId": self.a, "followeeId": ident(94)}]
        sink = m.FileSink(self.output, {"schema": m.SCHEMA})
        self.converter(sink=sink).convert()
        sink.finish()
        b = json.loads((self.output / "bundle.json").read_text())
        post = b["posts"][0]
        self.assertEqual((post["reply_id"], post["reblog_id"], post["media_ids"], post["recipient_ids"]), ("", "", [], []))
        self.assertEqual(b["reactions"], [])
        self.assertEqual(b["relations"], [])
        self.assertTrue(any(i["code"] == "remote_reference_trimmed" for i in b["issues"]))

    def test_atomic_private_bundle_and_nonzero_for_incomplete_assets(self):
        self.tables["drive_file"] = [{"id": ident(4), "userId": self.a, "storedInternal": True, "accessKey": "missing", "size": 7}]
        out = self.root / "final"
        staged = m.Staging.from_dict(self.tables)
        previous_umask = os.umask(0o022)
        try:
            def fake_fetch(run, plan, labels):
                run.staging = staged
                return 1
            with mock.patch.object(m.Run, "fetch", fake_fetch), contextlib.redirect_stdout(io.StringIO()) as stdout, contextlib.redirect_stderr(io.StringIO()):
                result = m.main(["--base-url", "https://source.test", "--id-method", "aidx", "--source-version", "2026.10.0", "--source-dir", str(self.source), "--output", str(out)])
            self.assertEqual(result, 2)
            self.assertEqual(out.stat().st_mode & 0o777, 0o700)
            self.assertEqual((out / "bundle.json").stat().st_mode & 0o777, 0o600)
            self.assertFalse((out / "bundle.json.partial").exists())
            self.assertNotIn("TEST_PRIVATE", stdout.getvalue())
            self.assertNotIn("$2b$", stdout.getvalue())
            self.assertEqual(json.loads((out / "bundle.json").read_text())["schema"], m.SCHEMA)
        finally:
            os.umask(previous_umask)

    def test_exported_at_is_fetch_start_kept_across_fetch_only(self):
        """--fetch-only, later build: source.exported_at is when the state was first fetched (UTC), not build time."""
        out, state = self.root / "bundle", self.root / "bundle.state"
        args = ["--base-url", "https://source.test", "--id-method", "aidx", "--source-version", "2026.10.0",
                "--source-dir", str(self.source), "--output", str(out)]
        started = {}

        def fetch_only(run, plan, labels):
            started["bulk"] = run.staging.get_meta("bulk_started")
            return 0
        with mock.patch.object(m.Run, "fetch", fetch_only), contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(m.main(args + ["--fetch-only"]), 0)
        self.assertTrue(started["bulk"])
        self.assertFalse(out.exists())
        # Pretend the bulk pass started hours ago, in a non-UTC offset.
        m.Staging(state / "staging.sqlite3").set_meta("bulk_started", "2026-10-19T09:02:03.456789+08:00")
        staged = m.Staging.from_dict(self.tables)

        def build(run, plan, labels):
            staged.set_meta("bulk_started", run.staging.get_meta("bulk_started"))
            run.staging = staged
            return 1
        for emit in ("bundle", "ndjson"):
            target = out if emit == "bundle" else self.root / "direct"
            stream = io.StringIO()
            with mock.patch.object(m.Run, "fetch", build), contextlib.redirect_stdout(stream), contextlib.redirect_stderr(io.StringIO()):
                m.main([a if a != str(out) else str(target) for a in args] + ["--state-dir", str(state), "--emit", emit])
            if emit == "bundle":
                header = json.loads((out / "bundle.json").read_text())
            else:
                header = json.loads(stream.getvalue().splitlines()[0])["header"]
            self.assertEqual(header["source"]["exported_at"], "2026-10-19T01:02:03.456789Z", emit)
        # A build-time fallback (no state) is still UTC with a Z suffix.
        c = self.converter()
        self.assertTrue(c.bundle["source"]["exported_at"].endswith("Z"))

    def test_utc_iso(self):
        self.assertEqual(m.utc_iso("2026-10-19T09:00:00+08:00"), "2026-10-19T01:00:00Z")
        self.assertEqual(m.utc_iso("2026-10-19T01:00:00"), "2026-10-19T01:00:00Z")
        self.assertEqual(m.utc_iso("2026-10-19T01:00:00.5Z"), "2026-10-19T01:00:00.500000Z")
        self.assertEqual(m.utc_iso(""), "")
        self.assertEqual(m.utc_iso("not a time"), "")

class RedactTwoFactorTests(unittest.TestCase):
    def test_two_factor_secrets_are_redacted(self):
        samples = ['row {"twoFactorSecret": "JBSWY3DPEHPK3PXP", "x": 1}',
                   "{'twoFactorBackupSecret': ['abcd1234', 'efgh5678'], 'a': 2}",
                   "twoFactorTempSecret=JBSWY3DPEHPK3PXP other",
                   'DETAIL: Key ("twoFactorSecret")=(JBSWY3DPEHPK3PXP) already exists.']
        for text in samples:
            out = m.redact(text)
            for secret in ("JBSWY3DPEHPK3PXP", "abcd1234", "efgh5678"):
                self.assertNotIn(secret, out)


class StagingSealTests(unittest.TestCase):
    def test_secret_fields_are_sealed_at_rest_and_opened_on_read(self):
        key = "ab" * 32
        with tempfile.TemporaryDirectory() as d, mock.patch.dict(os.environ, {"COMMUNITY_TRANSFER_STATE_KEY": key}):
            path = Path(d) / "staging.sqlite3"
            s = m.Staging(path)
            s.set_pk("user_profile", "userId")
            s.set_pk("access_token", None)
            s.upsert("user_profile", [{"userId": "u0", "password": "$2b$10$hashvalue", "twoFactorSecret": "JBSWY3DPEHPK3PXP",
                                       "twoFactorBackupSecret": ["code-one"], "usePasswordLessLogin": False}])
            s.upsert("user_keypair", [{"userId": "u0", "privateKey": "TEST_PRIVATE_KEY"}], "userId")
            row = {"userId": "u0", "token": "native-token"}
            s.upsert("access_token", [row])
            s.upsert("access_token", [row])  # keyed by plaintext: same row, not a duplicate
            self.assertEqual(s.count("access_token"), 1)
            p = s.get("user_profile", "u0")
            self.assertEqual(p["twoFactorSecret"], "JBSWY3DPEHPK3PXP")
            self.assertEqual(p["twoFactorBackupSecret"], ["code-one"])
            self.assertIs(p["usePasswordLessLogin"], False)
            self.assertEqual(next(s.rows("user_keypair"))["privateKey"], "TEST_PRIVATE_KEY")
            self.assertEqual(s.by("user_profile", "userId", "u0")[0]["password"], "$2b$10$hashvalue")
            self.assertEqual(s.get_meta("fields_sealed_v1"), "1")
            s.db.execute("PRAGMA wal_checkpoint(TRUNCATE)")
            disk = b"".join(p.read_bytes() for p in Path(d).iterdir())
            for secret in (b"hashvalue", b"JBSWY3DPEHPK3PXP", b"code-one", b"TEST_PRIVATE_KEY", b"native-token"):
                self.assertNotIn(secret, disk)
        with tempfile.TemporaryDirectory() as d, mock.patch.dict(os.environ, {"COMMUNITY_TRANSFER_STATE_KEY": key}):
            s = m.Staging(Path(d) / "staging.sqlite3")
            s.upsert("user_keypair", [{"userId": "u0", "privateKey": "TEST_PRIVATE_KEY"}], "userId")
            with mock.patch.dict(os.environ, {"COMMUNITY_TRANSFER_STATE_KEY": "cd" * 32}):
                other = m.Staging(Path(d) / "staging.sqlite3")
                with self.assertRaises(m.ExportError):
                    other.get("user_keypair", "u0")


if __name__ == "__main__":
    unittest.main()
