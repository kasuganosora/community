# Personal profile preferences

**Settings → Personal profile** edits the existing display name, biography,
images and discovery controls, plus location, birthday, default post language,
profile fields, follow greeting, reaction preferences, cat ears and bot status.
The QR button generates and downloads a profile QR code in the browser.

`PATCH /api/v1/accounts/update_credentials` accepts JSON and the existing
Mastodon form encodings. New optional fields are:

| Field | Values / limit |
| --- | --- |
| `location` | Public text, 128 characters |
| `birthday` | Public date, `YYYY-MM-DD`, or empty to clear |
| `language` | BCP-47 language tag, or empty for content-based detection |
| `fields_attributes` | Up to 16 ordered `{name, value}` objects; empty array clears |
| `followed_message` | 200 characters, available only to the owner and accepted followers |
| `reaction_acceptance` | `all`, `likeOnlyForRemote`, `nonSensitiveOnly`, `nonSensitiveOnlyForLocalLikeOnlyForRemote`, `likeOnly` |
| `is_cat` | Boolean; displays original Community cat-ear artwork |
| `bot` | Existing boolean; publishes an ActivityPub Service actor |

Omitted fields retain their values. Explicit empty strings, false booleans,
and an empty fields array clear their respective settings. Profile field names
are limited to 255 characters, values to 2048. Clients edit plain text from
`source.fields`, rather than resubmitting the rendered HTML in `fields`.

The default post language applies when a new post request omits `language`.
It does not alter the interface language. Reaction preferences apply to the
author's existing and new posts: disallowed emoji reactions are rejected;
ordinary favourites remain available, and earlier reactions can be removed.
The like-only setting accepts heart reactions. Remote policies depend on the
reactor's account origin, not the emoji's origin.

## Profile link verification

Saving profile fields queues verification in `profile_field_verifications`.
The save request does not wait for external pages. A public HTTP(S) field URL
is verified only when its fetched HTML contains an `a` or `link` element with
`rel="me"` pointing to that account's profile or actor URL. JavaScript-generated
links do not count. For example, the external page can contain:

```html
<a rel="me" href="https://social.example/@alice">My profile</a>
```

The existing DNS-pinned SSRF transport checks connections and redirects. A
page response is limited to 1 MiB; requests have short deadlines. Temporary
failures retry up to five times. Jobs survive process restarts. Saving again
starts a new check; replaced jobs cannot verify a newer, different field.
The public profile displays the resulting verification indicator on refresh.
Clients cannot set `verified_at` through the profile update API.

Cat status interoperates with Misskey's `isCat` ActivityPub extension.
Artwork and source-license review is recorded in
[the license note](../legal/misskey-cat-ears.md).
