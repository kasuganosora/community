<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import type { MastodonStatus } from '@/shared/types/mastodon'
import client from '@/shared/api/client'
import { formatCount, formatRelativeTime } from '@/shared/utils/format'
import { stripHtml as stripHtmlUtil } from '@/shared/utils/sanitize'
import LogoIcon from '@/shared/components/common/LogoIcon.vue'

const router = useRouter()

const publicTimeline = ref<MastodonStatus[]>([])
interface InstanceInfo {
  name: string
  description: string
  userCount: number
  statusCount: number
  domain: string
}

const instanceInfo = ref<InstanceInfo>({
  name: 'Community',
  description: 'A decentralized social platform',
  userCount: 0,
  statusCount: 0,
  domain: window.location.host,
})

// Computed to limit the timeline preview to 5 items
const previewStatuses = computed(() => publicTimeline.value.slice(0, 5))

interface InstanceResponse {
  title?: string
  uri?: string
  short_description?: string
  description?: string
  stats?: {
    user_count?: number
    status_count?: number
  }
}

onMounted(async () => {
  try {
    const [instanceRes, timelineRes] = await Promise.allSettled([
      client.get<InstanceResponse>('/api/v1/instance'),
      client.get<MastodonStatus[]>('/api/v1/timelines/public', { params: { limit: 20 } }),
    ])
    if (instanceRes.status === 'fulfilled') {
      const inst = instanceRes.value.data
      instanceInfo.value = {
        name: inst.title || inst.uri || 'Community',
        description: inst.short_description || inst.description || 'A decentralized social platform',
        userCount: inst.stats?.user_count || 0,
        statusCount: inst.stats?.status_count || 0,
        domain: inst.uri || window.location.host,
      }
    }
    if (timelineRes.status === 'fulfilled') {
      publicTimeline.value = timelineRes.value.data
    }
  } catch {
    // Promise.allSettled never rejects — this catch is for safety only
  }
})

function goLogin() {
  router.push({ name: 'login' })
}

function goRegister() {
  router.push({ name: 'register' })
}

function stripHtml(html: string | null | undefined): string {
  if (!html) return ''
  return stripHtmlUtil(html)
}
</script>

<template>
  <div class="landing">
    <!-- Decorative gradient background -->
    <div class="landing__gradient" />

    <div class="landing__content">
      <!-- Left column: Instance info + timeline preview -->
      <div class="landing__left">
        <!-- Instance card -->
        <div class="instance-card">
          <div class="instance-card__logo">
            <LogoIcon :size="64" />
          </div>

          <h1 class="instance-card__name">{{ instanceInfo.name }}</h1>
          <p class="instance-card__desc">{{ instanceInfo.description }}</p>

          <div class="instance-card__actions">
            <t-button
              theme="primary"
              block
              size="large"
              shape="round"
              @click="goRegister"
            >
              Register on this server
            </t-button>
            <t-button
              theme="default"
              block
              size="large"
              shape="round"
              variant="outline"
              class="btn-explore"
              disabled
              title="Coming soon"
            >
              Explore other servers
            </t-button>
            <t-button
              theme="default"
              block
              size="large"
              shape="round"
              variant="text"
              class="btn-login"
              @click="goLogin"
            >
              Login
            </t-button>
          </div>

          <div class="instance-card__stats">
            <div class="stat">
              <span class="stat__label">Users</span>
              <span class="stat__value">{{ formatCount(instanceInfo.userCount) }}</span>
            </div>
            <div class="stat">
              <span class="stat__label">Posts</span>
              <span class="stat__value">{{ formatCount(instanceInfo.statusCount) }}</span>
            </div>
          </div>
        </div>

        <!-- Public timeline preview -->
        <div class="timeline-preview">
          <h2 class="timeline-preview__title">Public Timeline</h2>
          <div class="timeline-preview__list">
            <div
              v-for="status in previewStatuses"
              :key="status.id"
              class="preview-post"
            >
              <t-avatar
                size="36"
                :image="status.account.avatar || undefined"
                shape="circle"
                class="preview-post__avatar"
              >
                {{ status.account.display_name?.charAt(0) ?? status.account.username?.charAt(0) ?? 'U' }}
              </t-avatar>
              <div class="preview-post__content">
                <div class="preview-post__header">
                  <span class="preview-post__name">{{ status.account.display_name }}</span>
                  <span class="preview-post__acct">@{{ status.account.acct }}</span>
                  <span class="preview-post__time">{{ formatRelativeTime(status.created_at) }}</span>
                </div>
                <div class="preview-post__text">{{ stripHtml(status.content) }}</div>
                <div class="preview-post__actions">
                  <span class="preview-action">
                    <t-icon name="chat" :size="14" />
                    {{ status.replies_count || '' }}
                  </span>
                  <span class="preview-action">
                    <t-icon name="swap" :size="14" />
                    {{ status.reblogs_count || '' }}
                  </span>
                  <span class="preview-action">
                    <t-icon name="heart" :size="14" />
                    {{ status.favourites_count || '' }}
                  </span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Right column: Scrolling feed -->
      <div class="landing__right">
        <div class="feed-column">
          <div
            v-for="status in publicTimeline"
            :key="'feed-' + status.id"
            class="feed-note"
          >
            <div class="feed-note__text">{{ stripHtml(status.content) }}</div>
            <div class="feed-note__reactions">
              <span
                v-if="status.favourites_count > 0"
                class="reaction"
              >
                <span class="reaction__emoji">❤️</span>
                <span class="reaction__count">{{ status.favourites_count }}</span>
              </span>
              <span
                v-if="status.reblogs_count > 0"
                class="reaction"
              >
                <span class="reaction__emoji">🔁</span>
                <span class="reaction__count">{{ status.reblogs_count }}</span>
              </span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.landing {
  position: relative;
  min-height: 100dvh;
  background: var(--color-bg-grouped);
  overflow: hidden;
}

/* A single, very soft wash of the accent behind the hero — depth without
   decoration. */
.landing__gradient {
  position: absolute;
  top: -18rem;
  left: 50%;
  width: 60rem;
  height: 40rem;
  transform: translateX(-50%);
  background: radial-gradient(closest-side, var(--color-accent-soft), transparent);
  pointer-events: none;
  z-index: 0;
}

.landing__content {
  position: relative;
  z-index: 1;
  display: flex;
  max-width: 68rem;
  margin: 0 auto;
  padding: calc(var(--space-12) + var(--safe-top)) var(--space-6) calc(var(--space-12) + var(--safe-bottom));
  gap: var(--space-10);
  min-height: 100dvh;
}

.landing__left {
  flex: 1;
  max-width: 28rem;
  display: flex;
  flex-direction: column;
  gap: var(--space-6);
}

.landing__right {
  flex: 1;
  max-width: 30rem;
  overflow-y: auto;
  max-height: calc(100dvh - var(--space-12) * 2);
  overscroll-behavior: contain;
  scrollbar-width: none;
  /* Content fades out at the scroll edges instead of hitting a hard line. */
  -webkit-mask-image: linear-gradient(transparent, #000 2rem, #000 calc(100% - 2rem), transparent);
  mask-image: linear-gradient(transparent, #000 2rem, #000 calc(100% - 2rem), transparent);
}

.landing__right::-webkit-scrollbar {
  display: none;
}

/* ---- Instance card --------------------------------------------------- */
.instance-card {
  padding: var(--space-8) var(--space-6) var(--space-6);
  border-radius: var(--radius-xl);
  background: var(--color-bg-row);
  box-shadow: var(--shadow-1);
  text-align: center;
}

.instance-card__logo {
  display: inline-grid;
  place-items: center;
  margin-bottom: var(--space-4);
  color: var(--color-label);
}

.instance-card__name {
  font-size: var(--text-title-1);
  line-height: var(--leading-title-1);
  letter-spacing: var(--tracking-title-1);
  font-weight: var(--weight-bold);
  color: var(--color-label);
  margin-bottom: var(--space-2);
}

.instance-card__desc {
  color: var(--color-label-secondary);
  font-size: var(--text-body);
  line-height: var(--leading-body);
  margin-bottom: var(--space-6);
  text-wrap: pretty;
}

.instance-card__actions {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin-bottom: var(--space-6);
}

.instance-card__actions :deep(.t-button) {
  margin: 0;
  font-weight: var(--weight-semibold);
}

.btn-explore {
  background: var(--color-fill) !important;
  border-color: transparent !important;
  color: var(--color-label-secondary) !important;
}

.btn-login {
  color: var(--color-accent-text) !important;
}

/* Stats: a two-cell inset group. */
.instance-card__stats {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--hairline);
  border-radius: var(--radius-md);
  background: var(--color-separator);
  overflow: hidden;
}

.stat {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-grouped);
}

.stat__label {
  font-size: var(--text-footnote);
  letter-spacing: var(--tracking-footnote);
  color: var(--color-label-secondary);
}

.stat__value {
  font-size: var(--text-title-2);
  line-height: var(--leading-title-2);
  letter-spacing: var(--tracking-title-2);
  font-weight: var(--weight-semibold);
  font-variant-numeric: tabular-nums;
  color: var(--color-label);
}

/* ---- Timeline preview -------------------------------------------------- */
.timeline-preview {
  border-radius: var(--radius-xl);
  background: var(--color-bg-row);
  box-shadow: var(--shadow-1);
  overflow: hidden;
}

.timeline-preview__title {
  padding: var(--space-4) var(--space-4) var(--space-2);
  font-size: var(--text-headline);
  letter-spacing: var(--tracking-headline);
  font-weight: var(--weight-semibold);
  color: var(--color-label);
}

.preview-post {
  position: relative;
  display: flex;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
}

.preview-post + .preview-post::before {
  content: '';
  position: absolute;
  top: 0;
  left: calc(var(--space-4) + 36px + var(--space-3));
  right: 0;
  height: var(--hairline);
  background: var(--color-separator);
}

.preview-post__avatar {
  flex-shrink: 0;
}

.preview-post__content {
  flex: 1;
  min-width: 0;
}

.preview-post__header {
  display: flex;
  align-items: baseline;
  gap: var(--space-1);
  margin-bottom: var(--space-0);
  overflow: hidden;
}

.preview-post__name {
  font-weight: var(--weight-semibold);
  font-size: var(--text-subhead);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  color: var(--color-label);
}

.preview-post__acct {
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.preview-post__time {
  margin-left: auto;
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.preview-post__text {
  font-size: var(--text-subhead);
  line-height: var(--leading-subhead);
  color: var(--color-label);
  overflow: hidden;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.preview-post__actions {
  display: flex;
  gap: var(--space-5);
  margin-top: var(--space-2);
}

.preview-action {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--color-label-secondary);
  font-size: var(--text-caption);
  font-variant-numeric: tabular-nums;
}

/* ---- Right column feed ------------------------------------------------- */
.feed-column {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-8) 0;
}

.feed-note {
  padding: var(--space-4);
  border-radius: var(--radius-lg);
  background: var(--color-bg-row);
  box-shadow: var(--shadow-1);
}

.feed-note__text {
  font-size: var(--text-body);
  line-height: var(--leading-body);
  color: var(--color-label);
  margin-bottom: var(--space-2);
  overflow: hidden;
  display: -webkit-box;
  -webkit-line-clamp: 4;
  -webkit-box-orient: vertical;
}

.feed-note__text:lang(zh) {
  line-height: var(--leading-cjk);
}

.feed-note__reactions {
  display: flex;
  gap: var(--space-2);
  flex-wrap: wrap;
}

.reaction {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-0) var(--space-2);
  border-radius: var(--radius-full);
  background: var(--color-fill);
  font-size: var(--text-caption);
}

.reaction__emoji {
  font-size: 0.8125rem;
}

.reaction__count {
  color: var(--color-label-secondary);
  font-variant-numeric: tabular-nums;
}

/* ---- Responsive -------------------------------------------------------- */
@media (max-width: 56.25rem) {
  .landing__content {
    flex-direction: column;
    align-items: center;
    gap: var(--space-6);
    padding-top: calc(var(--space-8) + var(--safe-top));
    padding-left: var(--gutter);
    padding-right: var(--gutter);
  }

  .landing__left,
  .landing__right {
    max-width: 30rem;
    width: 100%;
  }

  .landing__right {
    max-height: none;
    overflow-y: visible;
    -webkit-mask-image: none;
    mask-image: none;
  }

  .feed-column {
    padding: 0;
  }
}
</style>
