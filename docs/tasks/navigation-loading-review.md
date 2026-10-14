# Navigation loading review

Reviewed 2026-10-14 using the existing Emil design engineering guidance: immediate feedback, stable hierarchy, accessible status, and reduced motion.

| Before | After | Why |
| --- | --- | --- |
| Vue Router awaited each page's JavaScript dependencies, leaving the previous screen visible with no feedback. | A shared synchronous route wrapper commits navigation and shows the target title and loading status while retaining code splitting. | A click has an immediate, visible outcome. |
| The search fix had its own loading shell. | All 55 asynchronous layout/page entries use the shared mechanism. | Search, groups, settings, public/auth pages and administration behave consistently. |
| Slow administrator permission checks looked like an ignored click. | A neutral top progress indicator runs during navigation guards; protected content still waits for authorization. | Give feedback without bypassing permissions. |
| A cancelled navigation could change the document title to the destination. | Document titles update only after successful navigation. | Browser and assistive-technology context stays on the page actually displayed. |
| Wrapping each route separately would remount pages shared by several routes. | Shared views retain one wrapper identity and successful modules are cached. | Preserve group creation feedback, route props, page state, and normal back-navigation behavior. |

Implementation notes:

- `beforeResolve` starts parent and child imports together after authorization, without awaiting their results.
- Wrappers forward route props, attributes and slots. Existing composition leave/update guards continue to protect group drafts.
- Leaving before code arrives cannot mount the abandoned page. Load failures show one reload action.
- Permission feedback handles redirects, failures, overlapping navigations and reselecting the current page. Reduced-motion settings disable its animation.
- Pages in this project use composition route guards. An Options API route guard added in a future page would need explicit forwarding through the wrapper.

Validation:

- 24 targeted tests cover immediate navigation, cached views, route props and composition guards, concurrent layout/page loading, denied access, abandoned loads, recovery, shared component identity and navigation races.
- Chromium checks withheld the search module and group API separately. Both button submission and Enter navigated before either response was released; one search field remained, with no automatic group join.
- Chromium checks withheld administrator verification, both administrator modules, and the login module. Permission checks exposed only neutral progress; authorized parent and child imports started together; public authentication pages displayed loading immediately.
- The existing group browser flow passed at 1440 px and 390 px, including cancellation with unsent text and an attachment, old URL redirects, exact group preview and explicit join. No browser errors were observed.

Browser fixtures use controlled synthetic responses. They verify navigation and feedback, not remote federation availability.
