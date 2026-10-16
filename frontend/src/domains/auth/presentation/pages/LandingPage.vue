<script setup lang="ts">
import LanguagePicker from '@/shared/components/common/LanguagePicker.vue'
import { ref, onMounted, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { MastodonStatus } from '@/shared/types/mastodon'
import client from '@/shared/api/client'
import { formatCount } from '@/shared/utils/format'
import LogoIcon from '@/shared/components/common/LogoIcon.vue'
import PreviewStatus from '../components/PreviewStatus.vue'
import { cssImage, loadBranding, useBranding } from '@/shared/composables/useBranding'

import { t } from '@/shared/i18n'
const router = useRouter()
const route = useRoute()
// Admin branding (Misskey entrance settings): page style, visitor timeline
// and activity, icon, banner and background.
const { branding } = useBranding()
const simple = computed(() => branding.value.entrancePageStyle === 'simple')
const showTimeline = computed(() => !simple.value && branding.value.showTimelineForVisitor)
const showActivities = computed(() => !simple.value && branding.value.showActivitiesForVisitor)
const landingStyle = computed(() => branding.value.backgroundImageUrl
  ? { backgroundImage: cssImage(branding.value.backgroundImageUrl) }
  : undefined)

const publicTimeline = ref<MastodonStatus[]>([])
interface InstanceInfo {
  name: string
  description: string
  userCount: number
  statusCount: number
  domain: string
}

const instanceInfo = ref<InstanceInfo>({
  name: t('auth.LandingPage.s7'),
  description: t('auth.LandingPage.s8'),
  userCount: 0,
  statusCount: 0,
  domain: window.location.host,
})

// One fetch, split between the columns: the card shows the newest few and
// the feed on the right continues after them, so no post appears twice.
const PREVIEW_COUNT = 5
const previewStatuses = computed(() => publicTimeline.value.slice(0, PREVIEW_COUNT))
const feedStatuses = computed(() => publicTimeline.value.slice(PREVIEW_COUNT))

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
    const brand = await loadBranding()
    const wantsTimeline = brand.entrancePageStyle !== 'simple' && brand.showTimelineForVisitor
    const [instanceRes, timelineRes] = await Promise.allSettled([
      client.get<InstanceResponse>('/api/v1/instance'),
      wantsTimeline
        ? client.get<MastodonStatus[]>('/api/v1/timelines/public', { params: { limit: 25 } })
        : Promise.resolve({ data: [] as MastodonStatus[] }),
    ])
    if (instanceRes.status === 'fulfilled') {
      const inst = instanceRes.value.data
      instanceInfo.value = {
        name: inst.title || inst.uri || t('auth.LandingPage.s7'),
        description: inst.short_description || inst.description || t('auth.LandingPage.s8'),
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

// Keep the page the auth guard bounced us from, so login returns there.
function authQuery() {
  const r = route.query.redirect
  return typeof r === 'string' && r ? { redirect: r } : undefined
}

function goLogin() {
  router.push({ name: 'login', query: authQuery() }).catch(() => {})
}
</script>

<template>
  <div class="landing" :class="{ 'landing--simple': simple, 'landing--bg': !!branding.backgroundImageUrl }" :style="landingStyle">
    <!-- Decorative gradient background -->
    <div class="landing__gradient" />

    <div class="landing__content" :class="{ 'landing__content--single': !showTimeline }">
      <!-- Left column: Instance info + timeline preview -->
      <div class="landing__left">
        <!-- Instance card -->
        <div class="instance-card">
          <img v-if="branding.bannerUrl && !simple" class="instance-card__banner" :src="branding.bannerUrl" alt="" decoding="async" />
          <div class="instance-card__logo">
            <img v-if="branding.iconUrl" class="instance-card__icon" :src="branding.iconUrl" alt="" width="64" height="64" decoding="async" />
            <LogoIcon v-else :size="64" />
          </div>

          <h1 class="instance-card__name">{{ instanceInfo.name }}</h1>
          <p class="instance-card__desc">{{ instanceInfo.description }}</p>

          <div class="instance-card__actions">
            <t-button
              theme="primary"
              block
              size="large"
              shape="round"
              @click="goLogin"
            >
              {{ $t('auth.LandingPage.s1') }}
            </t-button>
            <t-button
              theme="default"
              block
              size="large"
              shape="round"
              variant="outline"
              class="btn-explore"
              href="https://joinmastodon.org/servers"
              target="_blank"
              rel="noopener"
            >
              {{ $t('auth.LandingPage.s2') }}
            </t-button>
          </div>

          <div v-if="showActivities" class="instance-card__stats">
            <div class="stat">
              <span class="stat__label">{{ $t('auth.LandingPage.s3') }}</span>
              <span class="stat__value">{{ formatCount(instanceInfo.userCount) }}</span>
            </div>
            <div class="stat">
              <span class="stat__label">{{ $t('auth.LandingPage.s4') }}</span>
              <span class="stat__value">{{ formatCount(instanceInfo.statusCount) }}</span>
            </div>
          </div>
          <div class="instance-card__lang"><LanguagePicker compact /></div>
        </div>

        <!-- Public timeline preview -->
        <div v-if="showTimeline" class="timeline-preview">
          <h2 class="timeline-preview__title">{{ $t('auth.LandingPage.s5') }}</h2>
          <p v-if="!previewStatuses.length" class="timeline-preview__empty">{{ $t('auth.LandingPage.s6') }}</p>
          <div class="timeline-preview__list">
            <PreviewStatus
              v-for="status in previewStatuses"
              :key="status.id"
              :status="status"
              :lines="3"
              class="preview-post"
            />
          </div>
        </div>
      </div>

      <!-- Right column: Scrolling feed -->
      <div v-if="showTimeline" class="landing__right">
        <div v-if="feedStatuses.length" class="feed-column">
          <PreviewStatus
            v-for="status in feedStatuses"
            :key="'feed-' + status.id"
            :status="status"
            :lines="6"
            class="feed-note"
          />
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.instance-card__lang { display: flex; justify-content: center; margin-top: var(--space-4, 16px); }
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

.landing--bg {
  background-size: cover;
  background-position: center;
}

.landing__content--single {
  justify-content: center;
  align-items: center;
}

.landing__content--single .landing__left {
  max-width: 26rem;
  width: 100%;
}

.instance-card__banner {
  display: block;
  width: calc(100% + var(--space-6) * 2);
  max-width: none;
  aspect-ratio: 3 / 1;
  object-fit: cover;
  margin: calc(var(--space-8) * -1) calc(var(--space-6) * -1) var(--space-4);
  border-radius: var(--radius-xl) var(--radius-xl) 0 0;
}

.instance-card__icon {
  width: 64px;
  height: 64px;
  border-radius: var(--radius-lg);
  object-fit: cover;
}

.landing--simple .instance-card__actions {
  margin-bottom: 0;
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

.timeline-preview__empty {
  padding: 0 var(--space-4) var(--space-4);
  color: var(--color-label-secondary);
  font-size: var(--text-subhead);
}

.preview-post {
  padding: var(--space-3) var(--space-4);
}

.preview-post + .preview-post {
  border-top: var(--hairline) solid var(--color-separator);
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
