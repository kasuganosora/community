# Misskey → Community field mapping (migration)

Source = Misskey PostgreSQL column; exporter = `scripts/migration/export_misskey.py` bundle field;
target = Community column written by `community-transfer` (`datatransfer`). Status as of the
commit that adds this file. "Gap" = Community has a place for it but the migration does not fill
it; "no target" = Community has no equivalent.

## Accounts (`user`, `user_profile`)

| Misskey | bundle | Community | status |
|---|---|---|---|
| username, host, uri, inbox, sharedInbox, url | username, domain, actor_uri, inbox, shared_inbox, url | accounts.username/domain/origin_id or local_actor_uri | ok |
| name, profile.description | display_name, note | accounts.display_name, note | ok |
| profile.fields + profile.verifiedLinks | fields[].verified_at | accounts_extra.fields (verified_at) | fixed (verified_at) |
| avatarId / bannerId | avatar_id / header_id | accounts.avatar / header (media) | ok |
| isLocked, isBot, isExplorable, profile.noCrawle | locked, bot, discoverable, noindex | accounts.locked, bot, discoverable, noindex | ok |
| profile.followingVisibility / followersVisibility | following_visibility, followers_visibility (+ hide_collections) | per-list visibility columns (0050d7c) | ok |
| isSuspended | suspended | accounts.is_ban | ok |
| isCat | is_cat | accounts.is_cat | **fixed** |
| profile.lang | language | accounts.language | **fixed** |
| profile.location | location | accounts.location (≤128) | **fixed** |
| profile.birthday | birthday | accounts.birthday (YYYY-MM-DD) | **fixed**; invalid values dropped by rule (see Value rules) |
| profile.followedMessage | followed_message | accounts.followed_message (≤200) | **fixed** |
| movedToUri | moved_to | accounts.moved_to (local + remote) | **fixed** (was local settings only) |
| alsoKnownAs (simple-array: comma-joined text) | also_known_as (split list) | accounts.also_known_as (local + remote) | **fixed** (round 9: was read as one value, all dropped) |
| requireSigninToViewContents | settings | accounts.require_signin_to_view | ok |
| makeNotesHiddenBefore / FollowersOnlyBefore, chatScope, isDeleted | settings | user_settings (local) / report issue | ok (policy) |
| avatarDecorations + avatar_decoration catalog | — | accounts.avatar_decorations, avatar_decorations table | gap (catalog import needed; decorations had no ownerless files on maid) |
| role, role_assignment (badges, colors) | private records | roles / role_users | gap (Misskey policies ≠ Community permissions: needs a decision) |
| profile.autoAcceptFollowed, user.hideOnlineStatus, lastActiveDate | auto_accept_followed, hide_online_status, last_active_at | account privacy columns (0050d7c) | ok |
| user.speakAsCat | speak_as_cat | account column (0050d7c) | ok |
| followers/following counts | relations | accounts_extra.followers_count/following_count (recounted from follows) | ok |
| pinned notes (user_note_pining), local users | posts[].pinned | pin_statuses | ok |
| pinned notes of remote users | — (not exported) | — | by design: the remote server's featured collection, refreshed by federation fetch |
| user_keypair, instance.actor / relay.actor | keys, system_role | account keys, system_settings | ok |

## Notes and content

| Misskey | bundle | Community | status |
|---|---|---|---|
| text (MFM) | text, extensions.format=mfm | status_contents.content (plain = MFM source); `_misskey_content` served from it | ok |
| cw | spoiler_text | status_contents.spoiler_text | ok |
| visibility public/home/followers/specified | visibility | statuses.visibility 0/1/2/3 (replies may be narrowed, reported) | ok |
| localOnly | local_only | statuses.local_only | ok |
| visibleUserIds | recipient_ids | status_to_accounts | ok |
| replyId | reply_id | statuses.in_reply_to_id | ok |
| renoteId (pure) | reblog_id | statuses.reblog_id | ok |
| renoteId + content (quote) | quote_id / quote_uri | statuses.quote_id / properties.import_quote_uri | ok |
| fileIds + drive_file.isSensitive | media_ids, sensitive | attachments, statuses.sensitive | ok |
| tags, emojis | tags, emoji_ids | status_tags, status_emojis | ok |
| channelId | archive_only | private archive record | ok (by design) |
| lang (forks only; upstream has none) | language | statuses.language | fixed (when present) |
| reactionAcceptance (per note), deleteAt | — | — | no target |
| note_reaction (unicode, `:name@.:`, `:name@host:`) | reactions | status_reactions (+emoji_id); missing custom emoji → archive | ok |
| poll choices/votes/multiple/expiresAt | polls | polls, poll_options (votes) | ok |
| poll_vote | poll_votes | poll_option_votes | ok |
| note_favorite | bookmarks | bookmark_statuses | ok |

## Emoji, instance

| Misskey | bundle | Community | status |
|---|---|---|---|
| emoji name, host, url, category, aliases, license, isSensitive | shortcode, domain, url, category, aliases, license, sensitive | emojis.* | ok |
| emoji.localOnly | local_only | emojis.local_only | **fixed** |
| emoji.roleIdsThatCanBeUsedThisEmojiAsReaction | — | emojis.reaction_role_ids | gap (roles not migrated) |
| meta iconUrl, app icons, banner, background, info/error/not-found images | media.site_assets | branding.* settings of the import target | ok |
| meta logoImageUrl, ads, flash | — | — | no target (listed in the report) |
| other meta settings | — | tootctl import-misskey-meta | separate decision |

## Value rules

One definition, in `scripts/migration/export_misskey.py` (`split_also_known_as`,
`valid_actor_uri`, `also_known_as_values`, `valid_birthday`). `verify_misskey_import.py` imports
those functions; the Go importer (`datatransfer/profile_extensions.go`: `splitAlsoKnownAs`,
`validActorURI`, `validBirthday`) applies the same rules, and both run the shared vectors in
`scripts/migration/testdata/misskey_value_rules.json`. Change all three together.

* **alsoKnownAs.** Upstream Misskey declares `user.alsoKnownAs` as TypeORM `simple-array`: one
  `text` value with the actor URIs joined by `,`. Also accepted (forks / future versions): `text[]`
  (a list, or the PostgreSQL literal `{a,b}`) and a JSON array (a list or its JSON text). Every
  element is split on `,`, trimmed; empty and repeated values are removed (source order kept).
  An element is stored only when it is an absolute `http(s)` URL with a host, no userinfo, no
  fragment, at most 2048 bytes; each other element is an info `profile_value_dropped`
  (`account.also_known_as`). Personal-archive imports never take `also_known_as`.
* **birthday.** Trimmed; stored only when it is `YYYY-MM-DD`, a real calendar date with year ≥ 1
  (Misskey accepts `0000-MM-DD`) and not later than one day after the import time. Anything else
  (year 0000, Feb 30, future dates) is an info `profile_value_dropped` (`account.birthday`).
* **Pinned notes.** Only local users' pins (`user_note_pining."userId"` local) are exported and
  imported. A remote user's pins are their server's featured collection, which the target
  refreshes when it fetches the actor; importing a snapshot of it would go stale. A bundle that
  still marks a remote post pinned (older exporter) is reported as info `remote_pin_skipped`.
  In round 9 maid.lat had 31 pins: 1 local (imported), 30 remote (skipped).
* **lastActiveDate.** Copied as read. The source keeps changing after extraction, so users active
  on Misskey after the read have a newer source value than the target; that is not a migration
  error.

## Checks

* `verify_misskey_import.py` 8a–8g: the seven profile columns above, per account, local/remote
  split. Expected values apply the value rules, so values the importer drops by rule are excluded
  from the expected set and printed as `[INFO]` with a count (8d: invalid birthdays; 8g: aliases
  without one storable URI). 8d / 8g fail when the source has set values in scope but none parsed
  as storable (guard against the round 9 `0 == 0` false pass).
* 8h–8m: account privacy columns. 8m (`lastActiveDate`) reports accounts whose source value is
  newer than the import as `[INFO]` (“active after extraction”) instead of failing: with
  `--extracted-at <bundle source.exported_at>` only source values later than that time are
  tolerated; without it, any source value newer than a non-empty target value.
* 9: pinned notes of local users, count compared; remote users' pins are `[INFO]` (skipped by design).
* `compare_misskey_fields.py`: notes, reactions, polls, pins (local users' pins per note; remote
  users' pins `[INFO]`), channel archiving, emoji metadata and meta assets (read-only; ids and
  counts only). It does not compare account profile values (that is 8a–8m).
