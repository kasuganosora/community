#!/usr/bin/env python3
"""Read-only Mastodon PostgreSQL -> private community-transfer/v1 directory.

Unlike export_mastodon.rb (which runs inside the original Rails environment),
this reads the Mastodon database directly, so the setup wizard can migrate a
server without a working Mastodon installation. Use standard PG* environment
/ PGPASSFILE (or --dsn without a password), never a password argument.

Tables are read with keyset pagination, one short read-only transaction per
page, into a resumable SQLite state directory (default <output>.state), then
bundle.json is written incrementally. By default only local accounts, their
statuses/media/relations and the remote accounts/statuses/emojis those
reference are exported; --include-remote-cache exports every row. Two-phase
flow: --fetch-only while live, stop writes, then --final-sync. The same
options as export_misskey.py apply (--batch-size, --max-rows-per-sec,
--batch-sleep, --progress-interval, --verbose).

Media (Paperclip keys such as media_attachments/files/000/000/001/original/x.png):
  --media-dir DIR                 Mastodon's public/system directory (copy)
  --object-storage-mode copy      bytes from --object-storage-dir (a bucket mirror)
  --object-storage-mode reference keep <--object-base-url>/<key> URLs, copy nothing
  --list-object-keys              print {"key","size"} lines for the bucket mirror and exit

Exit codes: 0 exported, 2 exported with blocking issues, 1 failed.
"""
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import sys
from urllib.parse import urlsplit

sys.path.insert(0, str(Path(__file__).resolve().parent))
from export_misskey import (QUIET, SECTIONS, Converter, ExportError, FileSink, Run, Staging, StagingMap,  # noqa: E402
                            StagingTables, add_common_args, origin, sid, summary)

SCHEMA = "community-transfer/v1"
VISIBILITY = {0: "public", 1: "unlisted", 2: "private", 3: "direct", 4: "limited"}
INSTANCE_ACTOR_ID = "-99"
ADMIN_PERMISSION = 1  # UserRole::FLAGS[:administrator]


def ts(value):
    """row_to_json renders timestamp without time zone (stored as UTC) without an offset."""
    if not value:
        return None
    value = str(value)
    if value.endswith("Z") or "+" in value[10:] or value[10:].count("-") > 0:
        return value
    return value + "Z"


def id_partition(id_):
    """Paperclip :id_partition (Mastodon pads to 9 digits, then groups of 3)."""
    digits = "%09d" % int(id_)
    return "/".join(digits[i:i + 3] for i in range(0, len(digits), 3))


def paperclip_key(cls, attachment, id_, filename, remote=False, style="original"):
    if not filename:
        return None
    key = "%s/%s/%s/%s/%s" % (cls, attachment, id_partition(id_), style, filename)
    return "cache/" + key if remote else key


class MastodonConverter:
    copy_asset = Converter.copy_asset

    def __init__(self, tables, base_url, output, media_dir=None, object_mode="", object_dir=None, object_base_url="",
                 snapshot="", max_media_bytes=8 << 30, sink=None, progress=QUIET):
        self.staging = tables if isinstance(tables, Staging) else Staging.from_dict(tables)
        self.tables = StagingTables(self.staging)
        self.progress = progress
        self.base = origin(base_url)
        self.output = Path(output)
        self.remaining = max_media_bytes
        self.copied = {}
        self.source_dir = Path(media_dir).resolve(strict=True) if media_dir else None
        if object_mode not in ("", "copy", "reference"):
            raise ExportError("unsupported object storage mode")
        self.object_mode = object_mode
        self.object_dir = Path(object_dir).resolve(strict=True) if object_dir else None
        if object_mode == "copy" and not self.object_dir:
            raise ExportError("object storage copy mode requires a bucket mirror directory")
        self.object_base = (object_base_url or "").rstrip("/")
        if object_mode == "reference" and not self.object_base:
            raise ExportError("reference mode requires --object-base-url")
        version = ""
        for r in self.tables.get("schema_migrations", []):
            version = max(version, sid(r.get("version")))
        header = {"schema": SCHEMA, "scope": "site", "source": {
            "platform": "mastodon", "version": "schema " + version if version else "", "base_url": self.base,
            "account_domain": urlsplit(self.base).hostname, "snapshot_id": snapshot,
            "exported_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")}}
        self.sink = sink
        self.bundle = sink.bundle if sink is not None else dict(header, **{key: [] for key in SECTIONS})
        self.header = header
        self.trim_references = sink is not None
        # Disk-backed lookups; users and roles are local-only and small.
        self.accounts = StagingMap(self.staging, "accounts")
        self.users = {sid(r["account_id"]): r for r in self.tables.get("users", []) if r.get("account_id") is not None}
        self.roles = {sid(r["id"]): r for r in self.tables.get("user_roles", [])}
        self.statuses = StagingMap(self.staging, "statuses")
        self.staging.db.execute("CREATE TEMP TABLE IF NOT EXISTS exported_media(id TEXT PRIMARY KEY)")

    # -- helpers ---------------------------------------------------------
    def known(self, table, id_):
        return not self.trim_references or not id_ or self.staging.has(table, id_)

    def trimmed(self, kind, id_, what):
        self.issue("info", "remote_reference_trimmed", kind, id_, "A reference to %s outside the local-only export was dropped; the raw source row keeps it." % what)

    def stage(self, label, table):
        self.progress.stage("build/" + label, self.staging.count(table))

    def issue(self, severity, code, kind, id_, message):
        self.bundle["issues"].append(dict(severity=severity, code=code, kind=kind, source_id=sid(id_), message=message))

    def local(self, account):
        return not account.get("domain")

    def actor(self, account):
        if self.local(account):
            if sid(account.get("id")) == INSTANCE_ACTOR_ID:
                return self.base + "/actor"
            return self.base + "/users/" + account.get("username", "")
        return account.get("uri") or ""

    def status_uri(self, s):
        if s.get("uri"):
            return s["uri"]
        a = self.accounts.get(sid(s.get("account_id")), {})
        return self.actor(a) + "/statuses/" + sid(s["id"])

    def asset(self, id_, owner, key, size=None, remote_url="", **extra):
        """Media entry for a Paperclip key according to the media mode, or None."""
        if not key:
            return None
        m = {"id": id_, "owner_id": sid(owner), "name": key.rsplit("/", 1)[-1], "size": int(size or 0), **extra}
        m.setdefault("content_type", "application/octet-stream")
        if remote_url:
            m["remote_url"] = remote_url
        if self.object_mode == "reference":
            m["remote_url"] = self.object_base + "/" + key
            m["original_url"] = m["remote_url"]
            m["storage_reference"] = True
            self.bundle["media"].append(m)
            return id_
        root = self.object_dir if self.object_mode == "copy" else self.source_dir
        try:
            m.update(self.copy_asset(key, int(size) if size else None, root=root))
        except (ExportError, OSError, ValueError):
            if remote_url:
                m["size"] = 0
                self.issue("warning", "remote_media_reference", "media", id_, "Cached remote media is missing; the original remote URL is kept as a reference.")
            else:
                self.issue("error", "media_bytes_unavailable", "media", id_, "Media bytes are missing from the source storage or inconsistent with the database.")
                return None
        self.bundle["media"].append(m)
        return id_

    # -- sections ----------------------------------------------------------
    def preserve(self):
        for table in sorted(self.tables):
            if table in ("schema_migrations", "ar_internal_metadata"):
                continue
            rows = self.tables.get(table)
            self.stage("records:" + table, table)
            for i, row in enumerate(rows):
                self.progress.advance()
                self.bundle["records"].append({"kind": "mastodon." + table, "id": sid(row.get("id") if row.get("id") is not None else "row:%d" % i),
                                               "owner_id": sid(row.get("account_id")), "data": row})
        self.issue("warning", "source_auth_material", "credentials", "", "Private raw records contain source authentication material. Do not publish this bundle.")

    def export_accounts(self):
        self.stage("accounts", "accounts")
        for id_, a in self.accounts.items():
            self.progress.advance()
            remote = not self.local(a)
            avatar = self.asset("account:%s:avatar" % id_, id_, paperclip_key("accounts", "avatars", id_, a.get("avatar_file_name"), remote),
                                     a.get("avatar_file_size"), a.get("avatar_remote_url") or "", content_type=a.get("avatar_content_type") or "image/png",
                                     created_at=ts(a.get("created_at")))
            header = self.asset("account:%s:header" % id_, id_, paperclip_key("accounts", "headers", id_, a.get("header_file_name"), remote),
                                     a.get("header_file_size"), a.get("header_remote_url") or "", content_type=a.get("header_content_type") or "image/png",
                                     created_at=ts(a.get("created_at")))
            local = self.local(a)
            u = self.users.get(id_)
            role = self.roles.get(sid((u or {}).get("role_id")), {})
            actor = self.actor(a)
            rec = {"id": id_, "username": a.get("username") or "", "domain": a.get("domain") or "", "local": local,
                   "system": id_ == INSTANCE_ACTOR_ID, "actor_uri": actor,
                   "url": (self.base + "/@" + (a.get("username") or "")) if local else (a.get("url") or ""),
                   "inbox": actor + "/inbox" if local else (a.get("inbox_url") or ""),
                   "shared_inbox": self.base + "/inbox" if local else (a.get("shared_inbox_url") or ""),
                   "display_name": a.get("display_name") or "", "note": a.get("note") or "", "note_html": not local,
                   "fields": [{"name": f.get("name", ""), "value": f.get("value", "")} for f in (a.get("fields") or []) if isinstance(f, dict)],
                   "locked": bool(a.get("locked")), "bot": a.get("actor_type") == "Service", "discoverable": bool(a.get("discoverable")),
                   "noindex": not bool(a.get("indexable", True)), "hide_collections": bool(a.get("hide_collections")),
                   "suspended": bool(a.get("suspended_at")), "admin": bool(int(role.get("permissions") or 0) & ADMIN_PERMISSION),
                   "created_at": ts(a.get("created_at")), "updated_at": ts(a.get("updated_at")),
                   "settings": {"source_actor_type": a.get("actor_type"), "source_moved_to_account_id": sid(a.get("moved_to_account_id")),
                                "source_also_known_as": a.get("also_known_as") or [], "source_silenced_at": ts(a.get("silenced_at"))}}
            if avatar:
                rec["avatar_id"] = avatar
            if header:
                rec["header_id"] = header
            if not local:
                rec["avatar_url"] = a.get("avatar_remote_url") or ""
                rec["header_url"] = a.get("header_remote_url") or ""
            self.bundle["accounts"].append(rec)
            if not actor:
                self.issue("error", "missing_actor_uri", "account", id_, "Remote account has no canonical actor URI.")
            if local and not rec["system"]:
                if not u:
                    self.issue("error", "local_actor_without_login", "accounts", id_, "Local actor has no user row; credentials are never manufactured.")
                    continue
                two_factor = bool(u.get("otp_required_for_login"))
                if two_factor:
                    self.issue("error", "two_factor_requires_adapter", "credentials", id_, "TOTP must remain enforced; the encrypted secret needs the source Rails keys.")
                if not u.get("encrypted_password"):
                    self.issue("error", "missing_password_verifier", "credentials", id_, "No portable password verifier.")
                self.bundle["credentials"].append({"account_id": id_, "email": u.get("email") or "", "email_verified": bool(u.get("confirmed_at")),
                                                   "password_hash": u.get("encrypted_password") or "", "password_scheme": "bcrypt",
                                                   "disabled": bool(u.get("disabled")) or u.get("approved") is False, "two_factor": two_factor})

    def export_keys(self):
        has_keypairs = bool(self.tables.get("keypairs"))
        self.stage("keys", "accounts")
        for id_, a in self.accounts.items():
            self.progress.advance()
            local = self.local(a)
            actor = self.actor(a)
            keys = self.staging.by("keypairs", "account_id", id_) if has_keypairs else []
            if keys:
                for k in sorted(keys, key=lambda k: int(k.get("id") or 0)):
                    algorithm = k.get("type") or "rsa"
                    usable = not k.get("revoked") and not k.get("expires_at")
                    if local and usable and algorithm != "rsa":
                        self.issue("error", "unsupported_active_key", "keys", id_, "An active non-RSA signing key requires target support.")
                    self.bundle["keys"].append({"account_id": id_, "key_id": k.get("uri") or actor + "#main-key", "public_key": k.get("public_key") or "",
                                                "private_key": (k.get("private_key") or "") if local else "", "algorithm": algorithm,
                                                "primary": algorithm == "rsa" and usable, "revoked": bool(k.get("revoked"))})
                continue
            if a.get("public_key"):
                self.bundle["keys"].append({"account_id": id_, "key_id": actor + "#main-key", "public_key": a["public_key"],
                                            "private_key": (a.get("private_key") or "") if local else "", "algorithm": "rsa", "primary": True})
            if local and not a.get("private_key"):
                self.issue("error", "missing_local_signing_key", "keys", id_, "Local actor lacks a usable RSA identity key; its identity is never regenerated.")

    def export_media(self):
        self.stage("media", "media_attachments")
        for m in self.tables.get("media_attachments", []):
            self.progress.advance()
            id_ = sid(m["id"])
            owner = self.accounts.get(sid(m.get("account_id")), {})
            remote = bool(owner) and not self.local(owner)
            meta = m.get("file_meta") or {}
            original = meta.get("original") or {}
            focus = meta.get("focus") or {}
            extra = {"content_type": m.get("file_content_type") or "application/octet-stream", "description": m.get("description") or "",
                     "blurhash": m.get("blurhash") or "", "width": int(original.get("width") or 0), "height": int(original.get("height") or 0),
                     "focus_x": float(focus.get("x") or 0), "focus_y": float(focus.get("y") or 0), "created_at": ts(m.get("created_at"))}
            if not m.get("file_file_name"):
                if remote and m.get("remote_url"):
                    self.bundle["media"].append({"id": id_, "owner_id": sid(m.get("account_id")), "name": "remote-" + id_, "size": 0,
                                                 "remote_url": m["remote_url"], **extra})
                    self.issue("warning", "remote_media_reference", "media", id_, "Uncached remote media keeps its remote URL; no bytes were fetched.")
                    done = id_
                else:
                    self.issue("error", "media_has_no_original", "media", id_, "Media record has no original file.")
                    done = None
            else:
                key = paperclip_key("media_attachments", "files", id_, m["file_file_name"], remote)
                done = self.asset(id_, m.get("account_id"), key, m.get("file_file_size"), m.get("remote_url") or "", **extra)
            if done:
                self.staging.db.execute("INSERT OR IGNORE INTO exported_media(id) VALUES(?)", (done,))

    def status_media(self, status_id):
        """Media of a status that were actually written to the bundle."""
        return [sid(m["id"]) for m in self.staging.by("media_attachments", "status_id", status_id)
                if self.staging.db.execute("SELECT 1 FROM exported_media WHERE id=?", (sid(m["id"]),)).fetchone()]

    def export_posts(self):
        pins, emoji_by_domain = set(), {}
        for r in self.tables.get("status_pins", []):
            pins.add(sid(r.get("status_id")))
        for e in self.tables.get("custom_emojis", []):
            if e.get("shortcode"):
                emoji_by_domain.setdefault(e.get("domain") or "", []).append((e["shortcode"], sid(e["id"])))
        has_mentions, has_tags = bool(self.tables.get("mentions")), bool(self.tables.get("statuses_tags"))
        self.stage("posts", "statuses")
        for id_, s in self.statuses.items():
            self.progress.advance()
            if not self.known("accounts", sid(s.get("account_id"))):
                self.trimmed("posts", id_, "an unexported author")
                continue
            mentions = [sid(r.get("account_id")) for r in (self.staging.by("mentions", "status_id", id_) if has_mentions else []) if not r.get("silent")]
            mentions = [x for x in mentions if self.known("accounts", x)]
            tags = []
            for r in (self.staging.by("statuses_tags", "status_id", id_) if has_tags else []):
                t = self.staging.get("tags", sid(r.get("tag_id")))
                if t and t.get("name"):
                    tags.append(t["name"])
            reply_id, reblog_id = sid(s.get("in_reply_to_id")), sid(s.get("reblog_of_id"))
            for ref, what in ((reply_id, "a reply target"), (reblog_id, "a boost target")):
                if not self.known("statuses", ref):
                    self.trimmed("posts", id_, what)
            reply_id = reply_id if self.known("statuses", reply_id) else ""
            reblog_id = reblog_id if self.known("statuses", reblog_id) else ""
            author = self.accounts.get(sid(s.get("account_id")), {})
            local = s.get("local") if s.get("local") is not None else self.local(author)
            vis = VISIBILITY.get(s.get("visibility"), "public")
            if vis == "limited":
                self.issue("error", "unsupported_limited_visibility", "posts", id_, "Limited visibility cannot be silently mapped.")
            order = s.get("ordered_media_attachment_ids") or []
            attached = self.status_media(id_)
            media = [sid(x) for x in order if sid(x) in set(attached)] or attached
            text = s.get("text") or ""
            body = text + (s.get("spoiler_text") or "")
            emojis = sorted({v for code, v in emoji_by_domain.get(author.get("domain") or "", []) if (":" + code + ":") in body}) if ":" in body else []
            p = {"id": id_, "author_id": sid(s.get("account_id")), "uri": self.status_uri(s),
                 "url": (self.base + "/@" + author.get("username", "") + "/" + id_) if local else (s.get("url") or ""),
                 "spoiler_text": s.get("spoiler_text") or "", "visibility": vis, "sensitive": bool(s.get("sensitive")),
                 "language": s.get("language") or "", "recipient_ids": mentions,
                 "reply_id": reply_id, "reblog_id": reblog_id, "media_ids": media,
                 "tags": tags, "emoji_ids": emojis, "conversation_id": sid(s.get("conversation_id")),
                 "pinned": id_ in pins, "archive_only": bool(s.get("deleted_at")),
                 "created_at": ts(s.get("created_at")), "updated_at": ts(s.get("updated_at"))}
            p["text" if local else "html"] = text
            self.bundle["posts"].append({k: v for k, v in p.items() if v not in ("", None, [])} | {"visibility": vis, "author_id": p["author_id"], "id": id_})

    def export_relations(self):
        for table, kind in (("follows", "follow"), ("follow_requests", "follow_request"), ("blocks", "block"), ("mutes", "mute")):
            self.stage("relations:" + table, table)
            for r in self.tables.get(table, []):
                self.progress.advance()
                if not (self.known("accounts", sid(r.get("account_id"))) and self.known("accounts", sid(r.get("target_account_id")))):
                    self.trimmed(kind, r.get("id"), "an unexported account")
                    continue
                target = self.accounts.get(sid(r.get("target_account_id")), {})
                rel = {"kind": kind, "account_id": sid(r.get("account_id")), "target_id": sid(r.get("target_account_id")),
                       "target_uri": self.actor(target) if target else "", "show_reblogs": r.get("show_reblogs", True) is not False,
                       "notify": bool(r.get("notify")), "created_at": ts(r.get("created_at"))}
                if r.get("uri"):
                    rel["activity_uri"] = r["uri"]
                if r.get("languages"):
                    rel["languages"] = r["languages"]
                self.bundle["relations"].append(rel)
        for r in self.tables.get("account_domain_blocks", []):
            self.bundle["relations"].append({"kind": "domain_block", "account_id": sid(r.get("account_id")), "target_domain": r.get("domain") or "",
                                             "show_reblogs": False, "created_at": ts(r.get("created_at"))})

    def export_collections(self):
        members = {}
        for r in self.tables.get("list_accounts", []):
            if self.known("accounts", sid(r.get("account_id"))):
                members.setdefault(sid(r.get("list_id")), []).append(sid(r.get("account_id")))
        replies = {0: "list", 1: "followed", 2: "none"}
        for l in self.tables.get("lists", []):
            self.bundle["lists"].append({"id": sid(l["id"]), "owner_id": sid(l.get("account_id")), "title": l.get("title") or "",
                                         "replies_policy": replies.get(l.get("replies_policy"), "list"), "exclusive": bool(l.get("exclusive")),
                                         "member_ids": members.get(sid(l["id"]), [])})
        self.stage("favourites", "favourites")
        for r in self.tables.get("favourites", []):
            self.progress.advance()
            if not (self.known("statuses", sid(r.get("status_id"))) and self.known("accounts", sid(r.get("account_id")))):
                continue
            self.bundle["reactions"].append({"id": sid(r["id"]), "account_id": sid(r.get("account_id")), "post_id": sid(r.get("status_id")), "created_at": ts(r.get("created_at"))})
        for r in self.tables.get("bookmarks", []):
            if not self.known("statuses", sid(r.get("status_id"))):
                continue
            self.bundle["bookmarks"].append({"account_id": sid(r.get("account_id")), "post_id": sid(r.get("status_id"))})
        polls = set()
        for p in self.tables.get("polls", []):
            if not self.known("statuses", sid(p.get("status_id"))):
                continue
            tallies = p.get("cached_tallies") or []
            poll = {"id": sid(p["id"]), "post_id": sid(p.get("status_id")), "multiple": bool(p.get("multiple")),
                    "expires_at": ts(p.get("expires_at")),
                    "options": [{"title": t, "votes": int(tallies[i]) if i < len(tallies) and tallies[i] else 0} for i, t in enumerate(p.get("options") or [])]}
            if not poll["expires_at"]:
                del poll["expires_at"]
            self.bundle["polls"].append(poll)
            polls.add(poll["id"])
        for v in self.tables.get("poll_votes", []):
            if self.trim_references and (sid(v.get("poll_id")) not in polls or not self.known("accounts", sid(v.get("account_id")))):
                continue
            self.bundle["poll_votes"].append({"poll_id": sid(v.get("poll_id")), "account_id": sid(v.get("account_id")), "option": int(v.get("choice") or 0), "created_at": ts(v.get("created_at"))})

    def export_emojis(self):
        categories = {sid(c["id"]): c.get("name") for c in self.tables.get("custom_emoji_categories", [])}
        for e in self.tables.get("custom_emojis", []):
            id_ = sid(e["id"])
            remote = bool(e.get("domain"))
            media = self.asset("emoji:" + id_, INSTANCE_ACTOR_ID, paperclip_key("custom_emojis", "images", id_, e.get("image_file_name"), remote),
                               e.get("image_file_size"), e.get("image_remote_url") or "", content_type=e.get("image_content_type") or "image/png")
            if not media and not remote:
                self.issue("error", "emoji_has_no_original", "emojis", id_, "Emoji image is missing; it is never silently dropped.")
            rec = {"id": id_, "shortcode": e.get("shortcode") or "", "domain": e.get("domain") or "", "url": e.get("uri") or e.get("image_remote_url") or "",
                   "category": categories.get(sid(e.get("category_id"))) or "", "disabled": bool(e.get("disabled"))}
            if media:
                rec["media_id"] = media
            self.bundle["emojis"].append(rec)

    def object_keys(self):
        """Every Paperclip key the export would read, for mirroring a bucket."""
        for id_, a in self.accounts.items():
            remote = not self.local(a)
            for att, col in (("avatars", "avatar"), ("headers", "header")):
                k = paperclip_key("accounts", att, id_, a.get(col + "_file_name"), remote)
                if k:
                    yield k, a.get(col + "_file_size") or 0
        for m in self.tables.get("media_attachments", []):
            owner = self.accounts.get(sid(m.get("account_id")), {})
            k = paperclip_key("media_attachments", "files", sid(m["id"]), m.get("file_file_name"), bool(owner) and not self.local(owner))
            if k:
                yield k, m.get("file_file_size") or 0
        for e in self.tables.get("custom_emojis", []):
            k = paperclip_key("custom_emojis", "images", sid(e["id"]), e.get("image_file_name"), bool(e.get("domain")))
            if k:
                yield k, e.get("image_file_size") or 0

    def convert(self):
        self.preserve()
        self.export_accounts()
        self.export_keys()
        self.export_media()
        self.export_posts()
        self.export_relations()
        self.export_collections()
        self.export_emojis()
        self.progress.finish()
        return self.bundle


LABELS = {"accounts": "accounts", "statuses": "statuses", "media_attachments": "media", "follows": "follows", "keypairs": "keys",
          "favourites": "favourites", "custom_emojis": "emojis", "mentions": "mentions", "statuses_tags": "tags"}
# Remote-content caches without an owner column; local-only mode never reads them whole.
SKIP_LOCAL = {"preview_cards", "preview_cards_statuses", "conversations", "tags", "pghero_space_stats", "ar_internal_metadata"}


def mastodon_plan(f, include_remote_cache=False):
    scans = []

    def scan(table, where="TRUE"):
        if table in f.cat:
            scans.append((table, where))
            f.scan(table, where)

    if include_remote_cache:
        for table in sorted(f.cat):
            scan(table)
        return scans
    LA = "(SELECT a.id FROM public.accounts a WHERE a.domain IS NULL)"
    LS = "(SELECT s.id FROM public.statuses s WHERE s.account_id IN %s)" % LA
    handled = set(SKIP_LOCAL)

    def explicit(table, where, *cols):
        handled.add(table)
        if f.exists(table, *cols):
            scan(table, where)

    explicit("accounts", "t.domain IS NULL", "domain")
    explicit("statuses", "t.account_id IN " + LA, "account_id")
    for table in ("follows", "follow_requests", "blocks", "mutes"):
        explicit(table, "t.account_id IN %s OR t.target_account_id IN %s" % (LA, LA), "account_id", "target_account_id")
    explicit("favourites", "t.account_id IN %s OR t.status_id IN %s" % (LA, LS), "account_id", "status_id")
    explicit("poll_votes", "t.account_id IN %s OR t.poll_id IN (SELECT p.id FROM public.polls p WHERE p.account_id IN %s)" % (LA, LA), "account_id", "poll_id")
    explicit("mentions", "t.account_id IN %s OR t.status_id IN %s" % (LA, LS), "account_id", "status_id")
    explicit("custom_emojis", "t.domain IS NULL", "domain")
    explicit("list_accounts", "t.list_id IN (SELECT l.id FROM public.lists l WHERE l.account_id IN %s)" % LA, "list_id")
    for table in sorted(f.cat):
        if table in handled:
            continue
        cols = f.cols(table)
        if "account_id" in cols:
            scan(table, "t.account_id IN " + LA)
        elif "status_id" in cols:
            scan(table, "t.status_id IN " + LS)
        else:
            scan(table)
    local = "AND CAST(json_extract(data, '$.account_id') AS TEXT) IN (SELECT k FROM rows WHERE tbl='accounts' AND json_extract(data, '$.domain') IS NULL)"
    f.refs("statuses", "in_reply_to_id", [("statuses", "id", False, local)], name="ref:statuses.replies")
    f.refs("statuses", "id", [("statuses", "in_reply_to_id", False, local), ("statuses", "reblog_of_id", False, local),
                              ("favourites", "status_id", False, ""), ("bookmarks", "status_id", False, ""), ("status_pins", "status_id", False, "")])
    f.refs("mentions", "status_id", [("statuses", "id", False, "")])
    f.refs("statuses_tags", "status_id", [("statuses", "id", False, "")])
    f.refs("tags", "id", [("statuses_tags", "tag_id", False, "")])
    f.refs("media_attachments", "status_id", [("statuses", "id", False, "")])
    f.refs("polls", "status_id", [("statuses", "id", False, "")])
    f.refs("accounts", "id", [("statuses", "account_id", False, ""), ("mentions", "account_id", False, ""),
                              ("follows", "account_id", False, ""), ("follows", "target_account_id", False, ""),
                              ("follow_requests", "account_id", False, ""), ("follow_requests", "target_account_id", False, ""),
                              ("blocks", "target_account_id", False, ""), ("mutes", "target_account_id", False, ""),
                              ("favourites", "account_id", False, ""), ("list_accounts", "account_id", False, ""),
                              ("poll_votes", "account_id", False, "")])
    f.refs("keypairs", "account_id", [("accounts", "id", False, "")])
    f.refs("custom_emojis", "domain", [("accounts", "domain", False, "")])
    return scans


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--base-url", required=True, help="the Mastodon server's public origin (LOCAL_DOMAIN/WEB_DOMAIN)")
    ap.add_argument("--output", required=True, type=Path, help="new private directory; must not already exist")
    ap.add_argument("--media-dir", type=Path, help="Mastodon public/system directory (local storage)")
    ap.add_argument("--object-storage-mode", choices=("copy", "reference"), default=None)
    ap.add_argument("--object-storage-dir", type=Path, help="local bucket mirror keyed by object key (copy mode)")
    ap.add_argument("--object-base-url", default="", help="public URL prefix of the bucket (S3_ALIAS_HOST), reference mode")
    ap.add_argument("--list-object-keys", action="store_true", help="print the media keys as JSON lines and exit")
    add_common_args(ap)
    args = ap.parse_args(argv)
    run = Run(args, "mastodon")
    try:
        origin(args.base_url)
        if not args.fetch_only and not args.list_object_keys and args.output.exists():
            raise ExportError("output %s already exists; choose a new directory (the state directory is reused)" % args.output)
        if not args.fetch_only and not args.list_object_keys and not args.object_storage_mode and not args.media_dir:
            raise ExportError("a media source is required (--media-dir or --object-storage-mode)")
        run.open_state()
        rows = run.fetch(mastodon_plan, LABELS)
        if args.fetch_only:
            print(json.dumps({"status": "fetched", "staged_rows": rows, "state_dir": str(run.state)}), flush=True)
            return 0
        if args.list_object_keys:
            run.stage = "listing object keys"
            args.output.mkdir(mode=0o700, parents=False, exist_ok=True)
            conv = MastodonConverter(run.staging, args.base_url, args.output)
            for key, size in conv.object_keys():
                print(json.dumps({"key": key, "size": int(size)}))
            return 0
        run.stage = "build: creating output"
        args.output.mkdir(mode=0o700, parents=False, exist_ok=False)
        run.stage = "build"
        conv = MastodonConverter(run.staging, args.base_url, args.output, args.media_dir, args.object_storage_mode or "",
                                 args.object_storage_dir, args.object_base_url, run.snapshot_id(), args.max_media_bytes, progress=run.progress)
        sink = FileSink(args.output, conv.header)
        conv.sink, conv.bundle, conv.trim_references = sink, sink.bundle, True
        conv.convert()
        run.stage = "build: writing bundle.json"
        sink.finish()
        return summary(sink)
    except (Exception, KeyboardInterrupt) as exc:  # noqa: BLE001 - every failure is reported with its stage
        return run.fail(exc)


if __name__ == "__main__":
    sys.exit(main())
