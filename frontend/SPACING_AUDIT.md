# Spacing Audit Report - Padding & Margin Check

## CSS Variable Reference
```css
--space-1: 4px   (--spacing-xs)
--space-2: 8px   (--spacing-sm)
--space-3: 12px  (--spacing-md)
--space-4: 16px  (--spacing-lg)
--space-5: 20px  (--spacing-xl)
--space-6: 24px
--space-8: 32px
--space-10: 40px
--space-12: 48px
```

## Files with Hard-coded Values

### 🔴 High Priority - Core Components

| File | Line | Current | Should Be | Notes |
|------|------|---------|-----------|-------|
| LeftSidebar.vue | 97 | padding: 0 var(--space-3) | ✅ OK | Already using variable |
| LeftSidebar.vue | 108, 245 | padding: 0 | ⚠️ Keep | Intentional reset |
| LeftSidebar.vue | 267 | padding: 0 var(--space-2) | ✅ OK | Already using variable |
| PostCard.vue | 128 | padding-left: 56px | ⚠️ Keep | Avatar offset, not spacing |
| PostActions.vue | 14 | margin-top: var(--space-3) | ✅ OK | Already using variable |

### 🟡 Medium Priority - Page Components

| File | Line | Current | Should Be | Status |
|------|------|---------|-----------|--------|
| LandingPage.vue | 250 | padding: 40px 24px | padding: var(--space-10) var(--space-6) | ❌ Needs fix |
| LandingPage.vue | 274 | padding: 32px | padding: var(--space-8) | ❌ Needs fix |
| LandingPage.vue | 279 | margin-bottom: 16px | margin-bottom: var(--space-4) | ❌ Needs fix |
| LandingPage.vue | 287 | margin-bottom: 8px | margin-bottom: var(--space-2) | ❌ Needs fix |
| LandingPage.vue | 293 | margin-bottom: 24px | margin-bottom: var(--space-6) | ❌ Needs fix |
| LandingPage.vue | 300 | margin-bottom: 24px | margin-bottom: var(--space-6) | ❌ Needs fix |
| LandingPage.vue | 334 | padding: 12px | padding: var(--space-3) | ❌ Needs fix |
| LandingPage.vue | 361 | padding: 16px | padding: var(--space-4) | ❌ Needs fix |
| LandingPage.vue | 368 | padding: 12px 16px | padding: var(--space-3) var(--space-4) | ❌ Needs fix |
| LandingPage.vue | 390 | margin-bottom: 4px | margin-bottom: var(--space-1) | ❌ Needs fix |
| LandingPage.vue | 433 | margin-top: 6px | margin-top: var(--space-2) | ❌ Needs fix |
| LandingPage.vue | 454 | padding: 16px | padding: var(--space-4) | ❌ Needs fix |
| LandingPage.vue | 466 | margin-bottom: 8px | margin-bottom: var(--space-2) | ❌ Needs fix |
| LandingPage.vue | 484 | padding: 2px 8px | padding: 2px var(--space-2) | ⚠️ Partial |

### 🟢 Lower Priority - Auth Pages

| File | Line | Current | Should Be | Status |
|------|------|---------|-----------|--------|
| LoginPage.vue | 115 | padding: 20px | padding: var(--space-5) | ❌ Needs fix |
| LoginPage.vue | 121 | padding: 32px | padding: var(--space-8) | ❌ Needs fix |
| LoginPage.vue | 126 | margin-bottom: 24px | margin-bottom: var(--space-6) | ❌ Needs fix |
| LoginPage.vue | 134 | margin-bottom: 32px | margin-bottom: var(--space-8) | ❌ Needs fix |
| LoginPage.vue | 139 | margin-bottom: 16px | margin-bottom: var(--space-4) | ❌ Needs fix |
| LoginPage.vue | 149 | padding: 0 12px | padding: 0 var(--space-3) | ❌ Needs fix |
| LoginPage.vue | 161 | margin-top: 24px | margin-top: var(--space-6) | ❌ Needs fix |
| LoginPage.vue | 168 | margin-left: 4px | margin-left: var(--space-1) | ❌ Needs fix |
| LoginPage.vue | 175 | margin-bottom: 16px | margin-bottom: var(--space-4) | ❌ Needs fix |

| RegisterPage.vue | 199 | margin-bottom: 16px | margin-bottom: var(--space-4) | ⚠️ Inline style |
| RegisterPage.vue | 286 | padding: 20px | padding: var(--space-5) | ❌ Needs fix |
| RegisterPage.vue | 303 | padding: 16px 20px | padding: var(--space-4) var(--space-5) | ❌ Needs fix |
| RegisterPage.vue | 312 | padding: 4px | padding: var(--space-1) | ❌ Needs fix |
| RegisterPage.vue | 328 | padding: 24px 40px | padding: var(--space-6) var(--space-10) | ❌ Needs fix |
| RegisterPage.vue | 334 | margin-bottom: 16px | margin-bottom: var(--space-4) | ❌ Needs fix |
| RegisterPage.vue | 341 | margin-bottom: 24px | margin-bottom: var(--space-6) | ❌ Needs fix |
| RegisterPage.vue | 353 | margin-bottom: 16px | margin-bottom: var(--space-4) | ❌ Needs fix |
| RegisterPage.vue | 361 | padding: 12px 16px | padding: var(--space-3) var(--space-4) | ❌ Needs fix |
| RegisterPage.vue | 369 | padding: 0 16px 8px | padding: 0 var(--space-4) var(--space-2) | ❌ Needs fix |
| RegisterPage.vue | 376 | padding: 8px 0 | padding: var(--space-2) 0 | ❌ Needs fix |
| RegisterPage.vue | 404 | padding: 12px 16px | padding: var(--space-3) var(--space-4) | ❌ Needs fix |
| RegisterPage.vue | 422 | padding: 4px 16px 12px | padding: var(--space-1) var(--space-4) var(--space-3) | ❌ Needs fix |
| RegisterPage.vue | 437 | margin-bottom: 20px | margin-bottom: var(--space-5) | ❌ Needs fix |
| RegisterPage.vue | 448 | margin-top: 8px | margin-top: var(--space-2) | ❌ Needs fix |
| RegisterPage.vue | 467 | margin-bottom: 4px | margin-bottom: var(--space-1) | ❌ Needs fix |
| RegisterPage.vue | 473 | margin-bottom: 20px | margin-bottom: var(--space-5) | ❌ Needs fix |
| RegisterPage.vue | 504 | margin-top: 4px | margin-top: var(--space-1) | ❌ Needs fix |

### 🟢 Other Components

| File | Line | Current | Should Be | Status |
|------|------|---------|-----------|--------|
| AppLayout.vue | 54 | padding: 0 16px | padding: 0 var(--space-4) | ❌ Needs fix |
| PostMedia.vue | 48 | margin-top: 12px | margin-top: var(--space-3) | ❌ Needs fix |
| SearchBox.vue | 41 | padding: 8px 0 | padding: var(--space-2) 0 | ❌ Needs fix |
| PostComposer.vue | 82 | padding: 12px 16px | padding: var(--space-3) var(--space-4) | ❌ Needs fix |
| PostComposer.vue | 123 | margin-top: 12px | margin-top: var(--space-3) | ❌ Needs fix |
| PostComposer.vue | 132 | padding: 8px | padding: var(--space-2) | ❌ Needs fix |
| RightSidebar.vue | 20 | padding-top: 8px | padding-top: var(--space-2) | ❌ Needs fix |
| ProfilePage.vue | 155 | margin-top: 72px | ⚠️ Keep | Specific positioning |
| ProfilePage.vue | 164 | margin-top: 4px | margin-top: var(--space-1) | ❌ Needs fix |
| ProfilePage.vue | 173 | margin-top: 12px | margin-top: var(--space-3) | ❌ Needs fix |
| ProfilePage.vue | 185 | margin-top: 12px | margin-top: var(--space-3) | ❌ Needs fix |
| TimelineFeed.vue | 86 | padding: 32px | padding: var(--space-8) | ❌ Needs fix |
| SearchResultsPage.vue | 132 | padding: 48px | padding: var(--space-12) | ❌ Needs fix |
| SearchResultsPage.vue | 164 | margin-top: 4px | margin-top: var(--space-1) | ❌ Needs fix |
| NotificationsPage.vue | 136 | padding: 12px 16px | padding: var(--space-3) var(--space-4) | ❌ Needs fix |
| NotificationsPage.vue | 158 | margin-top: 8px | margin-top: var(--space-2) | ❌ Needs fix |
| NotificationsPage.vue | 164 | margin-top: 4px | margin-top: var(--space-1) | ❌ Needs fix |
| PostDetailPage.vue | 108 | margin-bottom: 12px | margin-bottom: var(--space-3) | ❌ Needs fix |
| PostDetailPage.vue | 128 | margin-bottom: 16px | margin-bottom: var(--space-4) | ❌ Needs fix |
| TrendingCard.vue | 62, 68 | padding: 12px 16px | padding: var(--space-3) var(--space-4) | ❌ Needs fix |
| TrendingCard.vue | 96 | padding: 16px | padding: var(--space-4) | ❌ Needs fix |
| WhoToFollowCard.vue | 78, 86 | padding: 12px 16px | padding: var(--space-3) var(--space-4) | ❌ Needs fix |
| WhoToFollowCard.vue | 134 | padding: 16px | padding: var(--space-4) | ❌ Needs fix |
| SkeletonPost.vue | 38 | padding: 16px | padding: var(--space-4) | ❌ Needs fix |
| SkeletonPost.vue | 45, 59 | margin-bottom: 12px | margin-bottom: var(--space-3) | ❌ Needs fix |
| SkeletonUser.vue | 29 | padding: 12px 16px | padding: var(--space-3) var(--space-4) | ❌ Needs fix |
| AsyncComponent.vue | 91, 99 | padding: 32px | padding: var(--space-8) | ❌ Needs fix |

## Summary

- 🔴 **Critical**: 0 files
- 🟡 **High Priority**: 3 files (LandingPage, PostCard, LeftSidebar)
- 🟢 **Medium Priority**: Auth pages (LoginPage, RegisterPage)
- 🔵 **Low Priority**: Other components

**Total Issues**: ~80+ hardcoded values
**Already Using Variables**: ~10 values

## Recommendations

1. Replace all 4px → var(--space-1)
2. Replace all 8px → var(--space-2)
3. Replace all 12px → var(--space-3)
4. Replace all 16px → var(--space-4)
5. Replace all 20px → var(--space-5)
6. Replace all 24px → var(--space-6)
7. Replace all 32px → var(--space-8)
8. Replace all 40px → var(--space-10)
9. Replace all 48px → var(--space-12)

**Exceptions** (keep as-is):
- Specific positioning values (e.g., 56px avatar offset)
- Border widths (1px, 2px)
- Calculated values in expressions
- Zero values (0)
