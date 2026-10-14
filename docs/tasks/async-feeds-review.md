# Feed and list asynchronous interaction review

Date: 2026-10-14

This review follows the existing interface design rules: show feedback at the action's location, preserve useful content while refreshing the same query, and clear content when the query itself changes. It does not change the shared pagination contract.

| Before | After | Why |
| --- | --- | --- |
| A timeline with retained rows emitted `loadMore` when its error retry was clicked, even with no next page. | The error action emits `retry`; the owning page selects the appropriate request. It is disabled while loading. | A refresh failure must remain recoverable after the last page. |
| Switching profile tabs kept the previous tab's posts visible during the next request and after failure. | Changing tabs resets the query immediately and shows the new query's loading/error state. | Media and liked posts must not present results from another filter. |
| Switching notification filters left unrelated notification rows under the new filter. | Changing filters resets the old page and ignores its delayed responses. | A slow mention request should not show ordinary follow notifications. |
| Clearing notifications could submit repeated requests with no progress indication. | The confirmation shows “清空中…” and disables actions while pending. Failure restores the action; success invalidates in-flight pages. | Avoid duplicate mutations and old notifications reappearing from a late list response. |
| List index requests and creates could finish after an account change or navigation and write their old results. | List requests and creations use request/session guards, invalidate on unmount, reset on account changes, and show a creation busy label. | Retain correct account ownership and prevent stale completion feedback. |
| Post detail waited for both the main post and the entire reply context; a context error hid a successfully loaded post. | The main post renders as soon as it arrives. Replies have a separate loading state and local retry. Main-post authorization/deletion failures still hide the post and context. | Slow or unavailable replies should not block readable content. |
| A new reply could be overwritten when a previously pending context request completed. | Context results merge already visible local replies by ID. | Keeping the main post interactive while context loads must not lose new replies. |

## Verification

Focused component tests use deferred requests to exercise loading, failure, retry, account changes, rapid filter changes, and late completion without relying on a fast network:

- `TimelineFeed.spec.ts`: retained rows at the end of a timeline can retry; retry is disabled while loading; existing automatic pagination behavior remains covered.
- `ProfileLoading.spec.ts`: immediately clears the old filter; retries a failed media query; a late media response cannot overwrite the current posts tab.
- `NotificationsLoading.spec.ts`: filter loading and failure are distinct from empty results; late responses cannot cross filters; repeated clears are suppressed and failure restores the action.
- `ListsLoading.spec.ts`: old account reads/creates cannot reappear; stale failure cannot end a newer request's loading state.
- `PostDetailLoading.spec.ts`: main content appears before slow context; context retries do not re-fetch the main post; local replies merge once; a forbidden next post cannot expose old content or context.
- Existing `ProfilePage.spec.ts` follow-request behavior remains included.

The focused suite comprises 15 tests across 6 files. Targeted ESLint and `git diff --check` are included in the final verification.

## Coverage and limits

Code review covered timeline feed/store and home/explore/tag pages; profile, follow list and media grid; post detail; list index/detail and add-to-list dialog; notifications; shared pagination and HTTP timeout behavior. Search and group search belong to the parallel search review and were excluded here.

Home retry request selection and the add-to-list dialog's misleading empty state were reported to the root agent for separate fixes. This report's tests do not validate those separate changes. Browser screenshots, live-server latency, federation delivery, and a complete application build were not run by this subtask. These checks use synthetic component data and do not make production data changes.
