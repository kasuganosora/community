# Slow network behavior

The web client detects Data Saver, 2G/3G, RTT of at least 400 ms, or a reported
download speed below 1 Mbps when the browser exposes Network Information.
Browsers without this API retain the normal request policy.

- API requests allow 15 seconds normally and 45 seconds on a slow connection.
  Account lookup and account status reads use at least 30 seconds (or the slow
  budget, whichever is longer) so one stall retry still has time on a stuck
  client path. The global axios timeout is unchanged.
- Timeline, search, account lookup, and account status GETs may retry
  automatically, once, after no data has arrived for 8 seconds (30 seconds on
  a slow connection). Receiving any response body disables this retry, so
  partially downloaded data is kept. Both attempts share the original timeout
  budget. Cancellation stops the active attempt, clears timers/listeners, and
  prevents another request. A fast 404 or 410 is not retried.
- A profile with a cached account keeps that shell on screen while lookup
  refetches. A timeout or other transient failure leaves the shell up and
  shows retry there. 404 and 410 still replace it with the error state. The
  manual retry button remains when nothing is cached.
- Credential verification timeouts, network errors and server failures retain
  the local session. Startup shows an error and an in-place retry; a 401 still
  expires the session. Slow startup displays an explanation after 6 seconds.
- A failed route module shows a reload action that persists text drafts before
  retrying (browsers can cache a failed module import). A chunk error triggers
  automatic reload only when a newer server build is confirmed, with unsent
  work deferring that reload.
- Avatar images/decorations use native lazy loading. Slow connections use
  single-density avatar renditions and skip animated avatar hover prefetch.
  Video/audio files use `preload="none"`; play still loads the original.
- The service worker caches hashed assets when visited. It does not download
  every route/locale at install. Cached HTML can open after a 3-second network
  wait while a background request refreshes the shell. API responses, media,
  Matrix traffic, OAuth and federation endpoints remain outside this cache.
  The initial shell download is limited to 5 seconds.

## Verification

Use a production build (`npm run build`) because the development server does
not register the worker. Test in a fresh Chromium profile with Network set to
Slow 3G, then repeat after a normal visit with the cache enabled:

1. Inspect requests during worker installation: unused page and locale chunks
   should not be downloaded.
2. Hold a timeline, search, or account-lookup response for over 8 seconds
   on a reported 3G connection, then finish it. There should be one successful
   request. A cached profile stays visible during that refetch; a 404/410
   still replaces the page.
3. Stream part of a response before 8 seconds and finish later: the partial
   response should not be aborted or restarted.
4. Fail credential verification with 503 or a timeout. The error's retry button
   should recover without a document reload or clearing `auth_token`.
5. Fail one lazy route chunk, then restore the connection and reload. Navigation
   should recover with the session intact. A network failure alone should not
   trigger automatic deployment reload handling.
6. Load a timeline with video attachments. The video file should not download
   until played. Scroll to verify that lazy images still appear.
7. With a cached shell, delay the next HTML navigation over 3 seconds. The shell
   should render while the network request continues. Go offline and confirm
   that authenticated API data is not served from service-worker caches.

Automated coverage lives in `stallRetry.spec.ts`, `authStoreAccounts.spec.ts`,
`AppStartup.spec.ts`, `lazyRoute.spec.ts`, `useVersionSyncKill.spec.ts`,
`UserAvatar.spec.ts`, `PostMedia.spec.ts` and `serviceWorker.spec.ts`.
