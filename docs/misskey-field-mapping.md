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
| profile.birthday | birthday | accounts.birthday (YYYY-MM-DD) | **fixed** |
| profile.followedMessage | followed_message | accounts.followed_message (≤200) | **fixed** |
| movedToUri | moved_to | accounts.moved_to (local + remote) | **fixed** (was local settings only) |
| alsoKnownAs | also_known_as | accounts.also_known_as (local + remote) | **fixed** (was local settings only) |
| requireSigninToViewContents | settings | accounts.require_signin_to_view | ok |
| makeNotesHiddenBefore / FollowersOnlyBefore, chatScope, isDeleted | settings | user_settings (local) / report issue | ok (policy) |
| avatarDecorations + avatar_decoration catalog | — | accounts.avatar_decorations, avatar_decorations table | gap (catalog import needed; decorations had no ownerless files on maid) |
| role, role_assignment (badges, colors) | private records | roles / role_users | gap (Misskey policies ≠ Community permissions: needs a decision) |
| profile.autoAcceptFollowed, user.hideOnlineStatus, lastActiveDate | auto_accept_followed, hide_online_status, last_active_at | account privacy columns (0050d7c) | ok |
| user.speakAsCat | speak_as_cat | account column (0050d7c) | ok |
| followers/following counts | relations | accounts_extra.followers_count/following_count (recounted from follows) | ok |
| pinned notes (user_note_pining) | posts[].pinned | pin_statuses | ok |
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

## Checks

* `verify_misskey_import.py` 8a–8g: the seven account columns above, per account, local/remote split.
* `compare_misskey_fields.py`: notes, reactions, polls, pins, channel archiving, emoji metadata and
  meta assets (read-only; ids and counts only).
