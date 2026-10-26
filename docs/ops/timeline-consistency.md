# Timeline consistency

Home, public, tag and list feeds use `(created_at DESC, id DESC)` in SQL.
Their `max_id` identifies a real status whose publication time bounds the next
page. It is not a numeric range, and adding one to an id does not find a
preceding publication. Eviction history stores actual exclusive boundaries;
the first page is represented by an empty history cursor. Route snapshots
retain the current window's preceding boundary as well.

The API serializes publication times to milliseconds while SQL retains finer
precision. Equal API timestamps cannot prove SQL order by comparing ids. A
revalidation walk continues until it sees its retained boundary, strictly older
time, or the end of the feed. Unreached rows stay when the bounded walk runs out
of pages. Body rows and embedded reply/boost/quote ids are deduplicated separately.

Realtime frames contain ids, not authoritative post bodies. New ids remain
behind the new-post button. Window and head reads merge hints received during
the request instead of replacing them. An explicit reveal waits for any current
read, reuses its fresh head where possible, and is abandoned if its scope changes.

A `delete` frame can mean removal from a particular timeline (for example an
unfollow). It is not added to the global set of deleted objects. Each active
read tracks removals received after it started and filters stale returned rows,
including boosts of removed objects. Deletion/edit frames also invalidate the
prefetched head. A later fresh HTTP request can confirm membership again.

Edited visible objects are fetched by status id even if they are outside the
first page. Refreshes share one flight per id, reconcile another edit arriving
during that flight, and check the session epoch before committing. Pending
optimistic likes, boosts and bookmarks remain overlaid on edit responses.

Resetting or hydrating a list invalidates the previous scope's responses and
releases its flight slot. The previous request's cleanup cannot clear a newer
request's slot. Explicit 401/403/404 responses also clear retained rows and new-post
heads, including when a head-only safety poll detects the loss of access.
Account changes clear shared status state and route snapshots through the
existing session epoch mechanism.

Regression coverage:

- `timelineStore.spec.ts`: home/public eviction history, delayed window replies,
  realtime removals, queued reveals, account and tab isolation.
- `usePaginatedList.spec.ts`: restored snapshots, scope reset, live hints,
  deletions, prefetch invalidation and queued reveals.
- `revalidateTimeline.spec.ts`: late federated arrivals, revoked rows, embedded
  status deduplication and millisecond timestamp ties.
- `streamedEdits.spec.ts`: independent edit requests, last-edit reconciliation,
  account changes, pending actions and retained tag/list cards.
- Existing feed SQL/Redis and stream tests cover publication ordering,
  pagination, cache rebuilding and content-free stream forwarding.

The home new-post action is an in-flow row below the composer and above the
feed. Other timeline pages keep their navigation-header action. Neither overlays
post content. If the home row appears or disappears above the viewport, its
height change is compensated to preserve the reading position. The row collapses
before a successful reveal positions the new head. Reveal returns whether a
new head was committed; only a successful current-scope commit navigates to the
first post, after Vue renders it. Explicit navigation cancels virtual-window
anchor compensation and clears the old eviction spacer. Failures leave the
reading position unchanged and expose a retry; keyboard activation transfers
focus to the feed. A ready cache does not flash a network spinner.

A clicked head read acknowledges hints present when that request began. Hints
arriving later remain pending unless represented in the response. The bounded
hint buffer retains the most recent arrivals even when full, invalidating an
older cache key. A reveal waiting on prefetch or HTTP is abandoned on scope or
account change, including after the old page is unmounted.

Sidebar topic and follow suggestion refreshes retain their current DOM until a
replacement is ready (including relationship hydration). Failed background
refreshes retain the last successful result. Account changes still clear it
immediately. Visibility-triggered topic refreshes reuse results less than one
minute old and do not restart active requests.
