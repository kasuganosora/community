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

const error = ref<string | null>(null)

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
                :size="36"
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
  min-height: 100vh;
  background: var(--bg-primary);
  position: relative;
  overflow: hidden;
}

.landing__gradient {
  position: absolute;
  top: -200px;
  left: 50%;
  transform: translateX(-50%);
  width: 800px;
  height: 800px;
  background: radial-gradient(ellipse, rgba(180, 230, 30, 0.15) 0%, transparent 70%);
  pointer-events: none;
  z-index: 0;
}

.landing__content {
  position: relative;
  z-index: 1;
  display: flex;
  max-width: 1200px;
  margin: 0 auto;
  padding: var(--space-10) var(--space-6);
  gap: var(--space-12);
  min-height: 100vh;
}

.landing__left {
  flex: 1;
  max-width: 480px;
  display: flex;
  flex-direction: column;
  gap: var(--space-6);
}

.landing__right {
  flex: 1;
  max-width: 480px;
  overflow-y: auto;
  max-height: calc(100vh - var(--space-10));
}

/* Instance card */
.instance-card {
  background: var(--bg-secondary);
  border-radius: var(--radius-lg);
  padding: var(--space-8);
  text-align: center;
}

.instance-card__logo {
  margin-bottom: var(--space-4);
  color: var(--text-primary);
}

.instance-card__name {
  font-size: 28px;
  font-weight: 800;
  color: var(--text-primary);
  margin-bottom: var(--space-2);
}

.instance-card__desc {
  color: var(--text-secondary);
  font-size: var(--font-md);
  margin-bottom: var(--space-6);
}

.instance-card__actions {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  margin-bottom: var(--space-6);
}

.instance-card__actions :deep(.t-button--theme-primary) {
  background: linear-gradient(135deg, #86e01e, #67b90c) !important;
  border: none !important;
  color: #000 !important;
  font-weight: 700 !important;
}

.instance-card__actions :deep(.t-button--theme-primary:hover) {
  opacity: 0.9;
}

.btn-explore {
  border-color: var(--border-color) !important;
  color: var(--text-primary) !important;
}

.btn-login {
  color: var(--text-secondary) !important;
}

.instance-card__stats {
  display: flex;
  gap: 1px;
  background: var(--border-color);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.stat {
  flex: 1;
  background: var(--bg-elevated);
  padding: var(--space-3);
  display: flex;
  flex-direction: column;
  align-items: flex-start;
}

.stat__label {
  font-size: var(--font-xs);
  color: var(--text-secondary);
}

.stat__value {
  font-size: var(--font-xl);
  font-weight: 700;
  color: var(--accent-green);
}

/* Timeline preview */
.timeline-preview {
  background: var(--bg-secondary);
  border-radius: var(--radius-lg);
  overflow: hidden;
}

.timeline-preview__title {
  font-size: var(--font-lg);
  font-weight: 700;
  padding: var(--space-4);
  color: var(--text-primary);
}

.preview-post {
  display: flex;
  gap: 10px;
  padding: var(--space-3) var(--space-4);
  border-top: 1px solid var(--border-color);
  transition: background-color var(--transition-fast);
}

.preview-post:hover {
  background: var(--bg-hover);
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
  align-items: center;
  gap: var(--space-1);
  margin-bottom: var(--space-1);
  flex-wrap: nowrap;
  overflow: hidden;
}

.preview-post__name {
  font-weight: 700;
  font-size: var(--font-sm);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  color: var(--text-primary);
}

.preview-post__acct {
  color: var(--text-secondary);
  font-size: var(--font-xs);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.preview-post__time {
  color: var(--text-tertiary);
  font-size: var(--font-xs);
  white-space: nowrap;
  margin-left: auto;
}

.preview-post__text {
  font-size: var(--font-sm);
  color: var(--text-primary);
  line-height: 1.4;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.preview-post__actions {
  display: flex;
  gap: var(--space-4);
  margin-top: var(--space-2);
}

.preview-action {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--text-secondary);
  font-size: var(--font-xs);
}

/* Right column feed */
.feed-column {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.feed-note {
  background: var(--bg-secondary);
  border-radius: var(--radius-lg);
  padding: var(--space-4);
  transition: transform var(--transition-fast);
}

.feed-note:hover {
  transform: translateY(-1px);
}

.feed-note__text {
  font-size: var(--font-md);
  color: var(--text-primary);
  line-height: 1.5;
  margin-bottom: var(--space-2);
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 4;
  -webkit-box-orient: vertical;
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
  padding: 2px var(--space-2);
  border-radius: var(--radius-full);
  background: var(--bg-elevated);
  font-size: var(--font-xs);
  cursor: pointer;
  transition: background-color var(--transition-fast);
}

.reaction:hover {
  background: var(--bg-hover);
}

.reaction__emoji {
  font-size: 14px;
}

.reaction__count {
  color: var(--text-secondary);
}

/* Responsive */
@media (max-width: 900px) {
  .landing__content {
    flex-direction: column;
    align-items: center;
  }

  .landing__left,
  .landing__right {
    max-width: 100%;
    width: 100%;
  }

  .landing__right {
    max-height: none;
    overflow-y: visible;
  }
}
</style>
