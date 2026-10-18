<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { MessagePlugin } from '@/shared/ui/feedback'
import { displayDomain } from '@/shared/utils/accountDisplay'
import { moderationApi } from '../../infrastructure/moderationApi'
import { isValidDomain, normalizeDomain, parseLines } from '../../domain/lines'
import { useRelationList } from '../composables/useRelationList'
import { apiErrorMessage } from '../composables/apiErrorMessage'
import RelationOrderToggle from './RelationOrderToggle.vue'

import { t } from '@/shared/i18n'

const list = useRelationList<string>(
  (order, next) => moderationApi.listDomainBlocks(order, next),
  (domain) => domain,
  () => t('settings.DomainBlockSettings.s10'),
  { immediate: true },
)
const input = ref('')
const saving = ref(false)
const error = ref<string | null>(null)
const removing = ref('')

const entries = computed(() => parseLines(input.value, normalizeDomain))
const entriesHint = computed(() => t('settings.Blocks.domainsHint', [entries.value.length]))
const invalid = computed(() => entries.value.filter((d) => !isValidDomain(d)))

async function addDomains() {
  if (saving.value || removing.value) return
  const current = list.scope()
  error.value = null
  const domains = entries.value
  if (!domains.length || invalid.value.length) {
    error.value = invalid.value.length
      ? t('settings.Blocks.invalidDomains', [invalid.value.join(', ')])
      : t('settings.DomainBlockSettings.s11')
    return
  }
  saving.value = true
  const failed: string[] = []
  let firstError = ''
  let added = 0
  try {
    for (const domain of domains) {
      try {
        await moderationApi.blockDomain(domain)
        if (!current()) return
        added++
        list.prepend(domain)
      } catch (e) {
        failed.push(domain)
        if (!firstError) firstError = apiErrorMessage(e, t('settings.DomainBlockSettings.s13'))
      }
    }
    if (!current()) return
    // Keep only what still needs attention in the box.
    input.value = failed.join('\n')
    if (failed.length) error.value = t('settings.Blocks.someDomainsFailed', [failed.join(', '), firstError])
    if (added) MessagePlugin.success(added === 1 ? t('settings.DomainBlockSettings.s12') : t('settings.Blocks.domainsBlocked', [added]))
  } finally {
    if (current()) saving.value = false
  }
}

async function remove(value: string) {
  if (removing.value || saving.value) return
  const current = list.scope()
  removing.value = value
  try {
    await moderationApi.unblockDomain(value)
    if (!current()) return
    list.removeItem(value)
    MessagePlugin.success(t('settings.DomainBlockSettings.s14'))
  } catch (e) {
    if (current()) MessagePlugin.error(apiErrorMessage(e, t('settings.DomainBlockSettings.s15')))
  } finally {
    if (current()) removing.value = ''
  }
}

onMounted(() => { void list.ensureLoaded() })
</script>

<template>
  <form class="settings-form domain-blocks" data-testid="domain-block-settings" @submit.prevent="addDomains">
    <p class="settings-field__hint">{{ $t('settings.DomainBlockSettings.s1') }}</p>
    <div class="settings-field">
      <label for="domain-block" class="settings-field__label">{{ $t('settings.DomainBlockSettings.s7') }}</label>
      <textarea
        id="domain-block"
        v-model="input"
        class="settings-input settings-textarea"
        rows="4"
        inputmode="url"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
        aria-describedby="domain-block-hint"
        :placeholder="'example.com\nspam.example'"
      />
      <span id="domain-block-hint" class="settings-field__hint">{{ entriesHint }}</span>
    </div>
    <p v-if="error" class="settings-error" role="alert">{{ error }}</p>
    <div class="settings-actions">
      <button type="submit" class="btn btn--primary btn--sm" :disabled="saving || !!removing || !entries.length">{{ saving ? $t('settings.DomainBlockSettings.s8') : $t('settings.DomainBlockSettings.s9') }}</button>
    </div>

    <div class="domain-blocks__toolbar">
      <span class="settings-field__label">{{ $t('settings.Blocks.blockedDomains') }}</span>
      <RelationOrderToggle :model-value="list.order.value" :disabled="list.loading.value" @update:model-value="list.setOrder" />
    </div>
    <div v-if="list.error.value && !list.items.value.length" class="settings-error" role="alert">{{ list.error.value }} <button type="button" class="btn btn--ghost btn--sm" @click="list.reload">{{ $t('settings.DomainBlockSettings.s3') }}</button></div>
    <p v-else-if="list.loaded.value && !list.items.value.length" class="settings-field__hint">{{ $t('settings.DomainBlockSettings.s6') }}</p>
    <ul v-if="list.items.value.length" class="domain-list">
      <li v-for="item in list.items.value" :key="item" data-testid="domain-block-item">
        <span :title="item">{{ displayDomain(item) }}</span>
        <button type="button" class="btn btn--ghost btn--sm" :disabled="!!removing || saving" :aria-busy="removing === item" @click="remove(item)">{{ removing === item ? $t('settings.DomainBlockSettings.s4') : $t('settings.DomainBlockSettings.s5') }}</button>
      </li>
    </ul>
    <p v-if="list.loading.value" class="settings-field__hint" role="status">{{ $t('settings.DomainBlockSettings.s2') }}</p>
    <button v-if="list.next.value && !list.loading.value" type="button" class="btn btn--ghost btn--sm domain-blocks__more" @click="list.loadMore">{{ $t('settings.Blocks.loadMore') }}</button>
  </form>
</template>

<style scoped>
.settings-form { display: flex; flex-direction: column; gap: var(--space-3); padding-top: .75rem; }
.settings-textarea { min-height: 6.5rem; resize: vertical; line-height: 1.5; }
.domain-blocks__toolbar { display: flex; align-items: center; justify-content: space-between; gap: .75rem; flex-wrap: wrap; }
.domain-list { display: flex; flex-direction: column; list-style: none; }
.domain-list li { display: flex; align-items: center; justify-content: space-between; }
.domain-list li:last-child { border-bottom: 0; }
.domain-blocks__more { align-self: center; }
</style>
