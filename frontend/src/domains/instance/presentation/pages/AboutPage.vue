<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { instanceApi, DEFAULT_RULES, type InstanceInfo, type InstanceRule } from '../../infrastructure/instanceApi'
import { formatCount } from '@/shared/utils/format'
import { useBranding } from '@/shared/composables/useBranding'

import { t } from '@/shared/i18n'
const { branding } = useBranding()

const instance = ref<InstanceInfo | null>(null)
const rules = ref<InstanceRule[]>([])
const rulesAreDefault = ref(false)
const error = ref<string | null>(null)

onMounted(async () => {
  const [inst, rs] = await Promise.allSettled([instanceApi.getInstance(), instanceApi.getRules()])
  if (inst.status === 'fulfilled') instance.value = inst.value
  else error.value = inst.reason instanceof Error ? inst.reason.message : t('instance.AboutPage.s14')
  if (rs.status === 'fulfilled' && rs.value.length) {
    rules.value = rs.value
  } else {
    rules.value = DEFAULT_RULES
    rulesAreDefault.value = true
  }
})
</script>

<template>
  <main class="about-page">
    <div class="page-header page-header--with-back">
      <button class="page-header__back-btn" type="button" :aria-label="$t('instance.AboutPage.s1')" @click="$router.back()">
        <t-icon name="chevron-left" :size="20" />
      </button>
      <h1 class="page-header__title">{{ $t('instance.AboutPage.s2', [instance?.title || 'Community']) }}</h1>
    </div>

    <img v-if="branding.bannerUrl" class="about-banner" :src="branding.bannerUrl" alt="" decoding="async" />

    <p v-if="error" class="about-error" role="alert">{{ error }}</p>

    <section v-if="instance" class="about-section" aria-labelledby="about-intro">
      <h2 id="about-intro" class="about-section__title">{{ $t('instance.AboutPage.s3') }}</h2>
      <p>{{ instance.description || instance.short_description || $t('instance.AboutPage.s4') }}</p>
      <p class="about-meta">
        {{ instance.domain || instance.uri }}
        <template v-if="instance.stats">
          {{ $t('instance.AboutPage.s5', [formatCount(instance.stats.user_count ?? 0), formatCount(instance.stats.status_count ?? 0)]) }}
        </template>
      </p>
    </section>

    <section id="rules" class="about-section" aria-labelledby="about-rules">
      <h2 id="about-rules" class="about-section__title">{{ $t('instance.AboutPage.s6') }}</h2>
      <ol class="about-rules">
        <li v-for="r in rules" :key="r.id">{{ r.text }}</li>
      </ol>
      <p v-if="rulesAreDefault" class="about-meta">{{ $t('instance.AboutPage.s7') }}</p>
    </section>

    <section id="terms" class="about-section" aria-labelledby="about-terms">
      <h2 id="about-terms" class="about-section__title">{{ $t('instance.AboutPage.s8') }}</h2>
      <p>
        {{ $t('instance.AboutPage.s9') }}
      </p>
      <p v-if="instance?.contact?.email">
        {{ $t('instance.AboutPage.s10') }}<a :href="`mailto:${instance.contact.email}`">{{ instance.contact.email }}</a>
      </p>
    </section>
    <section class="about-section" aria-labelledby="about-software">
      <h2 id="about-software" class="about-section__title">{{ $t('instance.AboutPage.s11') }}</h2>
      <ul class="about-links">
        <li v-if="branding.repositoryUrl || instance?.source_url">
          {{ $t('instance.AboutPage.s12') }}<a :href="branding.repositoryUrl || instance?.source_url" target="_blank" rel="noopener noreferrer">{{ branding.repositoryUrl || instance?.source_url }}</a>
        </li>
        <li v-if="branding.feedbackUrl">
          {{ $t('instance.AboutPage.s13') }}<a :href="branding.feedbackUrl" target="_blank" rel="noopener noreferrer">{{ branding.feedbackUrl }}</a>
        </li>
      </ul>
    </section>
  </main>
</template>

<style scoped>
.about-page {
  max-width: var(--content-max-width, 40rem);
  margin: 0 auto;
  min-height: 100dvh;
}

.about-section {
  padding: var(--space-4) var(--gutter, var(--space-4));
  line-height: 1.6;
}

.about-section__title {
  font-size: var(--text-headline, 1.0625rem);
  font-weight: var(--weight-semibold, 600);
  margin-bottom: var(--space-2);
}

.about-banner {
  display: block;
  width: 100%;
  aspect-ratio: 3 / 1;
  object-fit: cover;
  background: var(--color-fill);
}

.about-links {
  list-style: none;
  padding: 0;
  overflow-wrap: anywhere;
}

.about-links li + li {
  margin-top: var(--space-1);
}

.about-rules {
  padding-left: 1.5em;
}

.about-rules li + li {
  margin-top: var(--space-1);
}

.about-meta,
.about-error {
  margin-top: var(--space-2);
  font-size: var(--text-footnote, var(--font-sm));
  color: var(--color-label-secondary, var(--text-secondary));
}
</style>
