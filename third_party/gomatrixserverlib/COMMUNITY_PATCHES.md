# Community compatibility patches

Source: https://github.com/matrix-org/gomatrixserverlib
Commit: `4fe595dc7f589ccf975b2a780d37be6e68758964`
Go version: `v0.0.0-20260716140101-4fe595dc7f58`

This directory retains the upstream source, tests, and license. Community uses
an explicit local `replace` because this upstream HEAD has reproducible Matrix
room version 12 state-resolution defects:

1. `ResolveStateConflictsV2New` does not initialize `resolvedCreate`, but its
   control-event ordering immediately requires the create event to determine
   privileged creator power. Initialize that immutable anchor from the complete
   auth-event set; do not pre-populate the authorization provider with the
   unconflicted state. Version 12 still uses state resolution v2.1.
2. The authorization fallback locates an auth event but inserts the candidate
   event instead. Insert the located `authEv` so resolution starting from empty
   state can authorize against the correct declared auth dependencies.
3. Remove a debug `fmt.Printf` that writes conflicted event IDs to stdout.

The caller supplies all branch state roots and their complete transitive auth
closure. Version 12's conflicted-subgraph traversal indexes both through the
same auth-event map.

The fork also includes `TestCommunityV12ConflictedBanUsesCreatorAndDeclaredAuth`,
a standalone reproduction of both resolver failures. All upstream root-package
tests are retained.

Community regression tests:

- `TestFederationV12ForkBanRejectsMergedMessage` exercises ban/join conflict,
  rejects a banned sender's merged message, and accepts a creator's message.
- `TestHistoricalStateForkUsesRoomVersionResolution/12` checks fork resolution.
- `TestFederationV12ExternalJoinAndIncomingEvents` checks domainless rooms and
  create events without a `room_id` field.

Remove the local replacement after an upstream release fixes these cases and
passes the retained tests. Do not disable authentication or downgrade v12 rooms
to v2 state resolution to work around these defects.
