<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { instanceApi, DEFAULT_RULES, type InstanceInfo, type InstanceRule } from '../../infrastructure/instanceApi'
import { formatCount } from '@/shared/utils/format'
import { useBranding } from '@/shared/composables/useBranding'

const { branding } = useBranding()

const instance = ref<InstanceInfo | null>(null)
const rules = ref<InstanceRule[]>([])
const rulesAreDefault = ref(false)
const error = ref<string | null>(null)

onMounted(async () => {
  const [inst, rs] = await Promise.allSettled([instanceApi.getInstance(), instanceApi.getRules()])
  if (inst.status === 'fulfilled') instance.value = inst.value
  else error.value = inst.reason instanceof Error ? inst.reason.message : '加载失败'
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
      <button class="page-header__back-btn" type="button" aria-label="返回" @click="$router.back()">
        <t-icon name="chevron-left" :size="20" />
      </button>
      <h1 class="page-header__title">关于 {{ instance?.title || 'Community' }}</h1>
    </div>

    <img v-if="branding.bannerUrl" class="about-banner" :src="branding.bannerUrl" alt="" decoding="async" />

    <p v-if="error" class="about-error" role="alert">{{ error }}</p>

    <section v-if="instance" class="about-section" aria-labelledby="about-intro">
      <h2 id="about-intro" class="about-section__title">简介</h2>
      <p>{{ instance.description || instance.short_description || '一个去中心化的社交网络实例。' }}</p>
      <p class="about-meta">
        {{ instance.domain || instance.uri }}
        <template v-if="instance.stats">
          · {{ formatCount(instance.stats.user_count ?? 0) }} 位本站用户 · {{ formatCount(instance.stats.status_count ?? 0) }} 条本站帖子
        </template>
      </p>
    </section>

    <section id="rules" class="about-section" aria-labelledby="about-rules">
      <h2 id="about-rules" class="about-section__title">服务器规则</h2>
      <ol class="about-rules">
        <li v-for="r in rules" :key="r.id">{{ r.text }}</li>
      </ol>
      <p v-if="rulesAreDefault" class="about-meta">管理员尚未配置规则，以上为默认规则。</p>
    </section>

    <section id="terms" class="about-section" aria-labelledby="about-terms">
      <h2 id="about-terms" class="about-section__title">服务条款</h2>
      <p>
        使用本站即表示你同意遵守上述服务器规则。违反规则的内容可能被删除，账号可能被限制或封禁。
        本站与其他 ActivityPub 服务器互通，你公开发布的内容可能被其他服务器缓存和展示。
      </p>
      <p v-if="instance?.contact?.email">
        联系管理员：<a :href="`mailto:${instance.contact.email}`">{{ instance.contact.email }}</a>
      </p>
    </section>
    <section class="about-section" aria-labelledby="about-software">
      <h2 id="about-software" class="about-section__title">软件与反馈</h2>
      <ul class="about-links">
        <li v-if="branding.repositoryUrl || instance?.source_url">
          源代码仓库：<a :href="branding.repositoryUrl || instance?.source_url" target="_blank" rel="noopener noreferrer">{{ branding.repositoryUrl || instance?.source_url }}</a>
        </li>
        <li v-if="branding.feedbackUrl">
          问题反馈：<a :href="branding.feedbackUrl" target="_blank" rel="noopener noreferrer">{{ branding.feedbackUrl }}</a>
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
