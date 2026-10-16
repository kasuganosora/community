#!/usr/bin/env python3
"""Read-only Misskey PostgreSQL -> private community-transfer/v1 directory.

Use standard PG* environment/PGPASSFILE, never a password command argument.
Source media must be a trusted consistent snapshot of Misskey's files directory.
No remote URL is fetched. See README.md for limitations and cutover requirements.
"""
import argparse
from collections import defaultdict
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import subprocess
import sys
import tempfile
from urllib.parse import unquote, urlsplit

SCHEMA = "community-transfer/v1"
METHODS = ("aid", "aidx", "meid", "meidg", "objectid", "ulid")
# Every public base table is read in ONE read-only repeatable-read transaction.
# format(%I) quotes catalog-derived identifiers; values never become SQL code.
SNAPSHOT_SQL = r"""\set ON_ERROR_STOP on
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '300s';
SET LOCAL lock_timeout = '5s';
SET LOCAL timezone = 'UTC';
SELECT json_build_object('_snapshot', txid_current_snapshot()::text);
SELECT format('SELECT json_build_object(''table'', %L, ''row'', row_to_json(t)) FROM %I.%I AS t;', table_name, table_schema, table_name)
FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE' ORDER BY table_name
\gexec
COMMIT;
"""


class ExportError(Exception):
    """A safe, non-secret diagnostic."""


def origin(value):
    u = urlsplit(value)
    if u.scheme not in ("https", "http") or not u.hostname or u.username or u.password or u.query or u.fragment or u.path not in ("", "/"):
        raise ExportError("base-url must be an explicit HTTP(S) origin without credentials/path")
    return value.rstrip("/")


def id_time(value, method):
    value = str(value)
    if method in ("aid", "aidx"):
        if not re.fullmatch(r"[0-9a-z]{%d}" % (10 if method == "aid" else 16), value):
            raise ValueError("invalid ID")
        ms = int(value[:8], 36) + 946684800000
    elif method in ("meid", "objectid"):
        if not re.fullmatch(r"[0-9a-f]{24}", value):
            raise ValueError("invalid ID")
        ms = int(value[:12], 16) - 0x800000000000 if method == "meid" else int(value[:8], 16) * 1000
    elif method == "meidg":
        if not re.fullmatch(r"g[0-9a-f]{23}", value):
            raise ValueError("invalid ID")
        ms = int(value[1:12], 16)
    elif method == "ulid":
        chars = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
        if len(value) != 26 or any(c not in chars for c in value) or value[0] > "7":
            raise ValueError("invalid ID")
        ms = 0
        for c in value[:10]:
            ms = ms * 32 + chars.index(c)
    else:
        raise ExportError("unsupported source ID method")
    return datetime.fromtimestamp(ms / 1000, timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def read_snapshot(output, max_records=1000000, max_bytes=512 << 20):
    tables = defaultdict(list)
    count = size = 0
    snapshot = None
    # stderr may contain sensitive server details; never replay it to console.
    with tempfile.TemporaryFile(dir=output) as errors:
        proc = subprocess.Popen(["psql", "-X", "-q", "-A", "-t", "-w", "-v", "ON_ERROR_STOP=1"],
                                stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=errors)
        try:
            proc.stdin.write(SNAPSHOT_SQL.encode())
            proc.stdin.close()
            for line in proc.stdout:
                size += len(line)
                if size > max_bytes:
                    raise ExportError("snapshot exceeds configured byte limit; no bundle was published")
                row = json.loads(line)
                if "_snapshot" in row:
                    snapshot = row["_snapshot"]
                    continue
                count += 1
                if count > max_records:
                    raise ExportError("snapshot exceeds configured record limit; no bundle was published")
                tables[row["table"]].append(row["row"])
            if proc.wait() != 0:
                raise ExportError("read-only psql snapshot failed; check PG configuration/server logs privately")
        finally:
            if proc.poll() is None:
                proc.kill()
                proc.wait()
            proc.stdout.close()
    if not snapshot or not tables.get("user"):
        raise ExportError("snapshot contains no Misskey user records")
    return dict(tables), snapshot


def sid(value):
    return "" if value is None else str(value)


class Converter:
    def __init__(self, tables, base_url, method, source_dir, output, version="", snapshot="", max_media_bytes=8 << 30,
                 object_mode="error", object_dir=None, object_base_url=""):
        self.tables = tables
        self.base = origin(base_url)
        if method not in METHODS:
            raise ExportError("unsupported source ID method")
        self.method = method
        self.source_dir = Path(source_dir).resolve(strict=True) if source_dir else None
        # Object storage (storedInternal=false): "error" (default) refuses a
        # metadata-only backup; "copy" reads bytes from object_dir, a local
        # mirror of the bucket keyed by accessKey; "reference" keeps the
        # original object URLs, for a target that keeps serving the same
        # bucket (no bytes are copied).
        if object_mode not in ("error", "copy", "reference"):
            raise ExportError("unsupported object storage mode")
        self.object_mode = object_mode
        self.object_dir = Path(object_dir).resolve(strict=True) if object_dir else None
        if object_mode == "copy" and not self.object_dir:
            raise ExportError("object storage copy mode requires a bucket mirror directory")
        self.object_base = (object_base_url or "").rstrip("/")
        self.output = Path(output)
        self.remaining = max_media_bytes
        self.copied = {}
        self.bundle = {"schema": SCHEMA, "scope": "site", "source": {
            "platform": "misskey", "version": version, "base_url": self.base,
            "snapshot_id": snapshot, "exported_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")}}
        for key in ("accounts", "credentials", "keys", "posts", "media", "relations", "lists", "reactions", "bookmarks", "polls", "poll_votes", "emojis", "records", "issues"):
            self.bundle[key] = []
        self.users = {sid(r["id"]): r for r in tables.get("user", [])}
        self.profiles = {sid(r["userId"]): r for r in tables.get("user_profile", [])}
        self.media_urls = {}
        self.emoji_ids = {(r.get("name"), r.get("host") or ""): sid(r["id"]) for r in tables.get("emoji", [])}
        self.system_users = {sid(r.get("userId")) for r in tables.get("system_account", [])}

    def issue(self, severity, code, kind, id_, message):
        self.bundle["issues"].append(dict(severity=severity, code=code, kind=kind, source_id=sid(id_), message=message))

    def time(self, row, key="id"):
        if row.get("createdAt"):
            return row["createdAt"]
        try:
            return id_time(row[key], self.method)
        except (ValueError, KeyError, OverflowError, OSError):
            self.issue("error", "invalid_source_time", "id", row.get(key), "Source ID cannot be decoded with the selected ID method.")
            return "0001-01-01T00:00:00Z"

    def actor(self, user):
        return self.base + "/users/" + sid(user["id"]) if user.get("host") is None else (user.get("uri") or "")

    def preserve(self):
        for table in sorted(self.tables):
            rows = self.tables[table]
            for i, row in enumerate(rows):
                self.bundle["records"].append({"kind": "misskey:" + table,
                    "id": sid(row.get("id") or row.get("userId") or row.get("noteId") or i),
                    "owner_id": sid(row.get("userId") or row.get("fromUserId")), "data": row})
            if rows:
                self.issue("warning", "source_records_preserved", table, "", "Complete source rows are preserved privately; preservation does not imply equivalent live support.")
        self.issue("warning", "source_auth_material", "credentials", "", "Private raw records contain source authentication/security material. Do not publish this bundle; sessions, MFA and unsupported settings require explicit migration policy.")
        self.issue("info", "source_id_method", "source", "", "Source ID timestamp method: " + self.method)

    def accounts(self):
        for id_, u in self.users.items():
            p = self.profiles.get(id_, {})
            a = {"id": id_, "username": u.get("username") or "", "local": u.get("host") is None,
                 "domain": u.get("host") or "", "actor_uri": self.actor(u), "system": id_ in self.system_users,
                 "display_name": u.get("name") or "", "note": p.get("description") or "", "note_html": False,
                 "fields": [{"name": f.get("name", ""), "value": f.get("value", "")} for f in p.get("fields", [])],
                 "avatar_id": sid(u.get("avatarId")), "header_id": sid(u.get("bannerId")),
                 "avatar_url": u.get("avatarUrl") or "", "header_url": u.get("bannerUrl") or "",
                 "emoji_ids": self.note_emoji_ids({"id": id_, "emojis": u.get("emojis") or [], "userHost": u.get("host")}, kind="account"),
                 "locked": bool(u.get("isLocked")), "bot": bool(u.get("isBot")), "discoverable": bool(u.get("isExplorable")),
                 "suspended": bool(u.get("isSuspended")), "noindex": bool(p.get("noCrawle")),
                 "hide_collections": any(p.get(k) not in (None, "public") for k in ("followingVisibility", "followersVisibility")), "created_at": self.time(u),
                 "settings": {k: v for k, v in u.items() if k in ("isDeleted", "chatScope", "requireSigninToViewContents", "makeNotesFollowersOnlyBefore", "makeNotesHiddenBefore", "movedToUri", "alsoKnownAs", "movedAt")}}
            for src, dst in (("url", "url"), ("inbox", "inbox"), ("sharedInbox", "shared_inbox"), ("updatedAt", "updated_at")):
                if u.get(src):
                    a[dst] = u[src]
            self.bundle["accounts"].append(a)
            if not a["actor_uri"]:
                self.issue("error", "missing_actor_uri", "account", id_, "Remote account has no canonical actor URI.")
            if a["local"] and not a["system"]:
                if not p:
                    self.issue("error", "missing_local_profile", "account", id_, "Local account has no profile/credential source row.")
                self.bundle["credentials"].append({"account_id": id_, "email": p.get("email") or "", "email_verified": bool(p.get("emailVerified")),
                    "password_hash": p.get("password") or "", "password_scheme": "bcrypt", "disabled": id_ in self.system_users or not bool(p.get("password")),
                    "two_factor": bool(p.get("twoFactorEnabled") or p.get("securityKeysAvailable") or p.get("usePasswordLessLogin")),
                    "extensions": ({k: p[k] for k in ("twoFactorEnabled", "twoFactorSecret", "twoFactorBackupSecret", "usePasswordLessLogin", "securityKeysAvailable") if p.get(k)}
                                   if p.get("twoFactorEnabled") or p.get("usePasswordLessLogin") or p.get("securityKeysAvailable") else {})})
        key_users = set()
        for k in self.tables.get("user_keypair", []):
            id_ = sid(k["userId"])
            u = self.users.get(id_)
            if not u or u.get("host") is not None:
                self.issue("error", "nonlocal_private_key", "key", id_, "Private key is not owned by a known local account.")
                continue
            key_users.add(id_)
            self.bundle["keys"].append({"account_id": id_, "key_id": self.actor(u) + "#main-key", "public_key": k.get("publicKey") or "",
                                        "private_key": k.get("privateKey") or "", "algorithm": "rsa", "primary": True})
        for id_, u in self.users.items():
            if u.get("host") is None and id_ not in key_users:
                self.issue("error", "missing_local_key", "account", id_, "Local account has no AP keypair; do not silently regenerate its identity.")
        for k in self.tables.get("user_publickey", []):
            self.bundle["keys"].append({"account_id": sid(k["userId"]), "key_id": k.get("keyId") or "", "public_key": k.get("keyPem") or "", "algorithm": "rsa"})

    def copy_asset(self, key, expected_size=None, root=None):
        root = root or self.source_dir
        if not root:
            raise ExportError("Local media source directory was not supplied.")
        if not isinstance(key, str) or not key or "\\" in key or "\x00" in key:
            raise ExportError("Invalid local media key.")
        parts = PurePosixPath(key).parts
        if key.startswith("/") or any(p in ("..", ".") for p in parts) or not parts:
            raise ExportError("Local media key escapes source directory.")
        # openat + O_NOFOLLOW at EVERY level rejects symlink traversal/races.
        root_fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        fd = None
        try:
            for part in parts[:-1]:
                child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=root_fd)
                os.close(root_fd)
                root_fd = child
            fd = os.open(parts[-1], os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=root_fd)
            before = os.fstat(fd)
            if not stat.S_ISREG(before.st_mode):
                raise ExportError("Local media is not a regular file.")
            if expected_size is not None and before.st_size != expected_size:
                raise ExportError("Source media size differs from database snapshot.")
            if key in self.copied:
                return self.copied[key]
            if before.st_size > self.remaining:
                raise ExportError("Media copy budget exceeded.")
            if shutil.disk_usage(self.output).free < before.st_size + (16 << 20):
                raise ExportError("Insufficient free disk space for media snapshot.")
            media_dir = self.output / "media"
            media_dir.mkdir(mode=0o700, exist_ok=True)
            digest = hashlib.sha256()
            size = 0
            with tempfile.NamedTemporaryFile(dir=media_dir, delete=False) as target:
                temp = Path(target.name)
                try:
                    with os.fdopen(fd, "rb") as source:
                        fd = None
                        while chunk := source.read(1 << 20):
                            size += len(chunk)
                            if size > before.st_size or size > self.remaining:
                                raise ExportError("Source media changed or exceeded copy budget.")
                            digest.update(chunk)
                            target.write(chunk)
                        after = os.fstat(source.fileno())
                    if (before.st_size, before.st_mtime_ns, before.st_ino) != (after.st_size, after.st_mtime_ns, after.st_ino) or size != before.st_size:
                        raise ExportError("Source media changed during snapshot copy.")
                    target.flush()
                    os.fsync(target.fileno())
                except BaseException:
                    temp.unlink(missing_ok=True)
                    raise
            sha = digest.hexdigest()
            final = media_dir / sha
            os.replace(temp, final)
            self.remaining -= size
            result = {"path": "media/" + sha, "sha256": sha, "size": size}
            self.copied[key] = result
            return result
        finally:
            if fd is not None:
                os.close(fd)
            os.close(root_fd)

    def copied_image_type(self, asset):
        # Derivative files have separate MIME from their originals. Inspect only
        # a fixed header of our verified private copy; do not fetch/decode URLs.
        with (self.output / asset["path"]).open("rb") as source:
            head = source.read(16)
        if head.startswith(b"\x89PNG\r\n\x1a\n"):
            return "image/png"
        if head.startswith(b"\xff\xd8\xff"):
            return "image/jpeg"
        if head[:6] in (b"GIF87a", b"GIF89a"):
            return "image/gif"
        if head.startswith(b"RIFF") and head[8:12] == b"WEBP":
            return "image/webp"
        if head[4:8] == b"ftyp" and head[8:12] in (b"avif", b"avis"):
            return "image/avif"
        return "application/octet-stream"

    def is_source_asset_url(self, url):
        u = urlsplit(url or "")
        return bool(u.scheme + "://" + u.netloc == self.base and u.path.startswith("/files/") and not u.query and not u.fragment)

    def object_key(self, url):
        """Bucket key of an object-storage URL under --object-base-url, or None."""
        if not self.object_base or not url or not url.startswith(self.object_base + "/"):
            return None
        rest = urlsplit(url)
        if rest.query or rest.fragment:
            return None
        return unquote(url[len(self.object_base) + 1:])

    def local_url_key(self, url):
        u = urlsplit(url or "")
        if u.scheme + "://" + u.netloc != self.base or not u.path.startswith("/files/") or u.query or u.fragment:
            raise ExportError("Local asset URL requires an explicit storage snapshot mapping.")
        return unquote(u.path[len("/files/"):])

    def media(self):
        for r in self.tables.get("drive_file", []):
            id_ = sid(r["id"])
            props = r.get("properties") or {}
            m = {"id": id_, "owner_id": sid(r.get("userId")), "name": r.get("name") or "", "content_type": r.get("type") or "application/octet-stream",
                 "size": r.get("size") or 0, "remote_url": r.get("url") or "", "description": r.get("comment") or "", "blurhash": r.get("blurhash") or "",
                 "width": props.get("width") or 0, "height": props.get("height") or 0, "created_at": self.time(r)}
            local_owner = r.get("userId") and self.users.get(sid(r["userId"]), {}).get("host") is None
            if r.get("storedInternal"):
                if self.is_source_asset_url(r.get("url")):
                    m["original_url"] = r["url"]
                try:
                    m.update(self.copy_asset(r.get("accessKey"), int(r.get("size") or 0)))
                except (ExportError, OSError, ValueError):
                    self.issue("error", "media_bytes_unavailable", "media", id_, "Internal media bytes are missing, unsafe, changed, over budget, or inconsistent with the database snapshot.")
                for name, key_col, url_col in (("thumbnail", "thumbnailAccessKey", "thumbnailUrl"), ("webpublic", "webpublicAccessKey", "webpublicUrl")):
                    # Misskey reserves keys even when no derivative was generated.
                    # A populated URL, not merely its reserved key, declares a variant.
                    if r.get(url_col):
                        variant = {"id": id_ + ":" + name, "owner_id": m["owner_id"], "name": name, "content_type": "application/octet-stream", "remote_url": r.get(url_col) or ""}
                        if self.is_source_asset_url(r.get(url_col)):
                            variant["original_url"] = r[url_col]
                        try:
                            variant.update(self.copy_asset(r[key_col]))
                            variant["content_type"] = self.copied_image_type(variant)
                        except (ExportError, OSError):
                            self.issue("error", "media_variant_unavailable", "media", variant["id"], "Derived asset bytes could not be safely copied from the snapshot.")
                        self.bundle["media"].append(variant)
            elif not r.get("isLink") and self.object_mode == "reference":
                self.issue("info", "object_storage_reference", "media", id_, "Object-storage media keeps its original URL; the bucket must stay available to the target.")
            elif not r.get("isLink") and self.object_mode == "copy":
                if r.get("url"):
                    m["original_url"] = r["url"]
                try:
                    m.update(self.copy_asset(r.get("accessKey"), int(r.get("size") or 0), root=self.object_dir))
                except (ExportError, OSError, ValueError):
                    self.issue("error", "media_bytes_unavailable", "media", id_, "Object-storage media bytes are missing from the bucket mirror or inconsistent with the database snapshot.")
            elif local_owner or not r.get("isLink"):
                self.issue("error", "external_storage_required", "media", id_, "This asset requires an object-storage/remote byte snapshot; metadata alone is not a complete site backup.")
            else:
                self.issue("warning", "remote_media_reference", "media", id_, "Remote linked media is preserved as a reference; bytes are not fetched or guaranteed available.")
            self.bundle["media"].append(m)
            for col in ("url", "webpublicUrl", "thumbnailUrl"):
                if r.get(col):
                    self.media_urls[r[col]] = id_

    def note_emoji_ids(self, row, kind="note"):
        result = []
        for raw in row.get("emojis") or []:
            name = str(raw).strip(":")
            domain = row.get("userHost") or ""
            if "@" in name:
                name, domain = name.rsplit("@", 1)
                if domain == ".":
                    domain = ""
            id_ = self.emoji_ids.get((name, domain))
            if id_:
                if id_ not in result:
                    result.append(id_)
            else:
                self.issue("warning", "missing_" + kind + "_emoji", kind, row.get("id"), "Referenced custom emoji was not found in the source emoji table; shortcode and source data are retained.")
        return result

    def posts(self):
        visibility = {"public": "public", "home": "unlisted", "followers": "private", "specified": "direct"}
        pinned = {sid(r["noteId"]) for r in self.tables.get("user_note_pining", [])}
        sensitive_files = {sid(r["id"]) for r in self.tables.get("drive_file", []) if r.get("isSensitive")}
        for r in self.tables.get("note", []):
            id_ = sid(r["id"])
            local = self.users.get(sid(r["userId"]), {}).get("host") is None
            p = {"id": id_, "author_id": sid(r["userId"]), "uri": r.get("uri") or (self.base + "/notes/" + id_ if local else ""),
                 "url": r.get("url") or "", "text": r.get("text") or "", "spoiler_text": r.get("cw") or "",
                 # Official NoteCreateService: reply.threadId ?? reply.id; roots have null threadId.
                 "conversation_id": "note-thread:" + sid(r.get("threadId") or id_),
                 "visibility": visibility.get(r.get("visibility"), "direct"), "local_only": bool(r.get("localOnly")),
                 "recipient_ids": [sid(x) for x in r.get("visibleUserIds", [])], "reply_id": sid(r.get("replyId")), "reblog_id": sid(r.get("renoteId")),
                 "media_ids": [sid(x) for x in r.get("fileIds", [])], "tags": r.get("tags") or [], "emoji_ids": self.note_emoji_ids(r), "pinned": id_ in pinned,
                 "created_at": self.time(r), "sensitive": any(sid(f) in sensitive_files for f in r.get("fileIds", [])),
                 "extensions": {"format": "mfm", "source": r}}
            if r.get("visibility") not in visibility:
                p["archive_only"] = True
                self.issue("error", "unknown_visibility", "note", id_, "Unknown source visibility must not be published.")
            if r.get("channelId"):
                p["archive_only"] = True
                self.issue("warning", "channel_note_archive", "note", id_, "Channel semantics need an explicit adapter; note remains archive-only.")
            if r.get("renoteId") and (r.get("text") or r.get("cw") or r.get("fileIds") or r.get("hasPoll")):
                p["archive_only"] = True
                self.issue("warning", "quote_note_archive", "note", id_, "Quote-note semantics must not silently become a pure boost.")
            self.bundle["posts"].append(p)
        rooms = {sid(r["id"]): r for r in self.tables.get("chat_room", [])}
        memberships = defaultdict(set)
        for r in self.tables.get("chat_room_membership", []):
            memberships[sid(r["roomId"])].add(sid(r["userId"]))
        # Official 2026.10 roomTimeline permits owner/current members to read all
        # history. Roles can additionally disable chat; do not approximate their
        # dynamic condition evaluator when a non-default read restriction exists.
        restricted_chat = any((r.get("policies") or {}).get("chatAvailability", "available") == "unavailable" for r in self.tables.get("meta", []))
        for role in self.tables.get("role", []):
            policy = (role.get("policies") or {}).get("chatAvailability")
            if policy and not policy.get("useDefault", True) and policy.get("value") == "unavailable":
                restricted_chat = True
        for table, author, recipient in (("chat_message", "fromUserId", "toUserId"), ("messaging_message", "userId", "recipientId")):
            for r in self.tables.get(table, []):
                sender = sid(r.get(author))
                target = sid(r.get(recipient))
                recipients = [target] if target else []
                archived = not bool(target)
                conversation = table + ":pair:" + ":".join(sorted((sender, target)))
                room_id = sid(r.get("toRoomId"))
                if table == "chat_message" and room_id:
                    room = rooms.get(room_id)
                    if room:
                        recipients = sorted(memberships[room_id] | {sid(room["ownerId"])})
                        archived = False
                    conversation = table + ":room:" + room_id
                elif r.get("groupId"):
                    conversation = table + ":group:" + sid(r["groupId"])
                # Author access is intrinsic to target direct posts. A departed
                # room sender is still permitted their own messages by source
                # searchMessages(fromUserId), without granting other old senders.
                if table == "chat_message" and restricted_chat:
                    archived = True
                post = {"id": table + ":" + sid(r["id"]), "author_id": sender, "text": r.get("text") or "",
                    "uri": r.get("uri") or "", "visibility": "direct", "recipient_ids": recipients,
                    "media_ids": [sid(r["fileId"])] if r.get("fileId") else [], "conversation_id": conversation,
                    "created_at": self.time(r), "archive_only": archived, "local_only": not bool(r.get("uri")), "extensions": r}
                self.bundle["posts"].append(post)
                if archived:
                    self.issue("warning", "chat_archive_only", table, r["id"], "Chat target or dynamic role read permissions require an explicit adapter; source data is retained privately.")
            if self.tables.get(table):
                self.issue("warning", "chat_policy_review", table, "", "Direct messages preserve participants and conversation identity; read receipts, reactions, moderator access and future membership/role policy need explicit target support. No new federation is authorized.")

    def relationships(self):
        for table, kind, source, target in (("following", "follow", "followerId", "followeeId"), ("follow_request", "follow_request", "followerId", "followeeId"),
                                             ("blocking", "block", "blockerId", "blockeeId"), ("muting", "mute", "muterId", "muteeId")):
            for r in self.tables.get(table, []):
                relation = {"kind": kind, "account_id": sid(r[source]), "target_id": sid(r[target]), "show_reblogs": True,
                            "notify": r.get("notify") not in (None, False, "none"), "activity_uri": r.get("requestId") or "", "created_at": self.time(r)}
                if kind in ("follow", "follow_request") and not relation["activity_uri"] and self.users.get(sid(r[source]), {}).get("host") is None:
                    relation["activity_uri"] = self.base + "/follows/" + sid(r[source]) + "/" + sid(r[target])
                if kind == "mute" and r.get("expiresAt"):
                    relation["expires_at"] = r["expiresAt"]
                self.bundle["relations"].append(relation)

        members = defaultdict(list)
        for r in self.tables.get("user_list_membership", []):
            members[sid(r["userListId"])].append(sid(r["userId"]))
        for r in self.tables.get("user_list", []):
            self.bundle["lists"].append({"id": sid(r["id"]), "owner_id": sid(r["userId"]), "title": r.get("name") or "", "member_ids": members[sid(r["id"])]})
        self.bundle["bookmarks"] = [{"account_id": sid(r["userId"]), "post_id": sid(r["noteId"])} for r in self.tables.get("note_favorite", [])]
        self.bundle["reactions"] = [{"id": sid(r["id"]), "account_id": sid(r["userId"]), "post_id": sid(r["noteId"]), "emoji": r["reaction"], "created_at": self.time(r)} for r in self.tables.get("note_reaction", [])]
        for r in self.tables.get("poll", []):
            votes = r.get("votes") or []
            choices = r.get("choices") or []
            if len(votes) != len(choices) or any(not isinstance(v, int) or v < 0 for v in votes):
                self.issue("error", "invalid_poll_counts", "poll", r["noteId"], "Poll option/count arrays are not aligned nonnegative counts.")
                continue
            p = {"id": sid(r["noteId"]), "post_id": sid(r["noteId"]), "multiple": bool(r.get("multiple")), "options": [{"title": title, "votes": votes[i]} for i, title in enumerate(choices)]}
            if r.get("expiresAt"):
                p["expires_at"] = r["expiresAt"]
            self.bundle["polls"].append(p)
        self.bundle["poll_votes"] = [{"poll_id": sid(r["noteId"]), "account_id": sid(r["userId"]), "option": r["choice"], "created_at": self.time(r)} for r in self.tables.get("poll_vote", [])]

    def emojis(self):
        for r in self.tables.get("emoji", []):
            id_ = sid(r["id"])
            url = r.get("originalUrl") or r.get("publicUrl") or ""
            e = {"id": id_, "shortcode": r.get("name") or "", "domain": r.get("host") or "", "url": r.get("publicUrl") or url,
                 "media_id": self.media_urls.get(url, ""), "category": r.get("category") or "", "aliases": r.get("aliases") or [],
                 "sensitive": bool(r.get("isSensitive")), "license": r.get("license") or ""}
            object_key = self.object_key(url)
            if r.get("host") is None and not e["media_id"] and object_key and self.object_mode == "reference":
                self.issue("info", "object_storage_reference", "emoji", id_, "Object-storage emoji keeps its original URL.")
            elif r.get("host") is None and not e["media_id"] and object_key and self.object_mode == "copy":
                asset_id = "emoji:" + id_
                m = {"id": asset_id, "owner_id": "", "name": r.get("name") or "", "content_type": r.get("type") or "application/octet-stream", "remote_url": url}
                try:
                    m.update(self.copy_asset(object_key, root=self.object_dir))
                    m["original_url"] = url
                    e["media_id"] = asset_id
                    self.bundle["media"].append(m)
                except (ExportError, OSError):
                    self.issue("error", "emoji_bytes_unavailable", "emoji", id_, "Object-storage emoji bytes are missing from the bucket mirror.")
            elif r.get("host") is None and not e["media_id"]:
                asset_id = "emoji:" + id_
                m = {"id": asset_id, "owner_id": "", "name": r.get("name") or "", "content_type": r.get("type") or "application/octet-stream", "remote_url": url}
                try:
                    m.update(self.copy_asset(self.local_url_key(url)))
                    m["original_url"] = url
                    e["media_id"] = asset_id
                    self.bundle["media"].append(m)
                except (ExportError, OSError):
                    self.issue("error", "emoji_bytes_unavailable", "emoji", id_, "Local emoji bytes need a safe local/object-storage snapshot mapping.")
            self.bundle["emojis"].append(e)

    def convert(self):
        self.preserve()
        self.accounts()
        self.media()
        self.posts()
        self.relationships()
        self.emojis()
        return self.bundle


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--base-url", required=True)
    ap.add_argument("--id-method", required=True, choices=METHODS, help="exact config.id from source Misskey")
    ap.add_argument("--source-version", required=True)
    ap.add_argument("--source-dir", type=Path, help="trusted consistent Misskey files snapshot; never an untrusted upload")
    ap.add_argument("--output", required=True, type=Path, help="new private directory; must not already exist")
    ap.add_argument("--max-records", type=int, default=1000000)
    ap.add_argument("--max-snapshot-bytes", type=int, default=512 << 20)
    ap.add_argument("--max-media-bytes", type=int, default=8 << 30)
    ap.add_argument("--object-storage-mode", choices=("error", "copy", "reference"), default="error",
                    help="media stored in object storage: refuse (default), copy from --object-storage-dir, or keep original URLs")
    ap.add_argument("--object-storage-dir", type=Path, help="local mirror of the source bucket, keyed by object key (copy mode)")
    ap.add_argument("--object-base-url", default="", help="public URL prefix of the source bucket (Misskey objectStorageBaseUrl)")
    args = ap.parse_args(argv)
    try:
        origin(args.base_url)
        if min(args.max_records, args.max_snapshot_bytes, args.max_media_bytes) <= 0:
            raise ExportError("export limits must be positive")
        os.umask(0o077)
        args.output.mkdir(mode=0o700, parents=False, exist_ok=False)
        tables, snapshot = read_snapshot(args.output, args.max_records, args.max_snapshot_bytes)
        bundle = Converter(tables, args.base_url, args.id_method, args.source_dir, args.output, args.source_version, snapshot, args.max_media_bytes,
                           args.object_storage_mode, args.object_storage_dir, args.object_base_url).convert()
        tmp = args.output / "bundle.json.partial"
        with tmp.open("x", encoding="utf-8") as f:
            json.dump(bundle, f, ensure_ascii=False, separators=(",", ":"))
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, args.output / "bundle.json")
        errors = sum(i["severity"] == "error" for i in bundle["issues"])
        print(json.dumps({"status": "blocked" if errors else "exported_with_review_required", "source_records": len(bundle["records"]), "errors": errors,
                          "warnings": sum(i["severity"] == "warning" for i in bundle["issues"])}))
        return 2 if errors else 0
    except (ExportError, OSError, ValueError, KeyError, TypeError):
        # Never print raw exception text: a DB error, path or bad record can contain secrets.
        print("Export failed; no complete migration is claimed. Check source configuration and private partial output.", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
