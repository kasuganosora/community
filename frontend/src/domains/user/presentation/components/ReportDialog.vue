<script setup lang="ts">
import { ref, watch } from 'vue'
import { MessagePlugin } from '@/shared/ui/feedback'
import { userApi } from '../../infrastructure/userApi'

import { t } from '@/shared/i18n'
const props = defineProps<{
  accountId: string
  statusId?: string
}>()

const open = defineModel<boolean>({ default: false })

const categories = [
  { value: 'spam', label: t('user.ReportDialog.s7') },
  { value: 'violation', label: t('user.ReportDialog.s8') },
  { value: 'legal', label: t('user.ReportDialog.s9') },
  { value: 'other', label: t('user.ReportDialog.s10') },
]

const category = ref('other')
const comment = ref('')
const sending = ref(false)

watch(open, (value) => {
  if (!value) return
  category.value = 'other'
  comment.value = ''
})

async function submit() {
  if (sending.value) return
  sending.value = true
  try {
    await userApi.report(props.accountId, comment.value.trim(), category.value, props.statusId ? [props.statusId] : [])
    MessagePlugin.success(t('user.ReportDialog.s11'))
    open.value = false
  } catch (e) {
    MessagePlugin.error(e instanceof Error && e.message ? e.message : t('user.ReportDialog.s12'))
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <t-dialog v-model:visible="open" :header="$t('user.ReportDialog.s1')" :footer="false" width="440px" @close="open = false">
    <form class="report" @submit.prevent="submit">
      <label class="report__field">
        <span>{{ $t('user.ReportDialog.s2') }}</span>
        <select v-model="category" class="report__select">
          <option v-for="item in categories" :key="item.value" :value="item.value">{{ item.label }}</option>
        </select>
      </label>
      <label class="report__field">
        <span>{{ $t('user.ReportDialog.s3') }}</span>
        <textarea v-model="comment" class="report__text" rows="4" maxlength="1000" :placeholder="$t('user.ReportDialog.s4')" />
      </label>
      <div class="report__actions">
        <button type="button" class="btn btn--ghost btn--sm" @click="open = false">{{ $t('user.ReportDialog.s5') }}</button>
        <button type="submit" class="btn btn--danger btn--sm" :disabled="sending">{{ $t('user.ReportDialog.s6') }}</button>
      </div>
    </form>
  </t-dialog>
</template>

<style scoped>
.report { display: flex; flex-direction: column; gap: var(--space-3); }
.report__field { display: flex; flex-direction: column; gap: var(--space-1); font-size: var(--text-footnote); }
.report__select, .report__text {
  width: 100%;
  box-sizing: border-box;
  font: inherit;
  color: #1c1c1e;
  background: #fff;
  color-scheme: light;
  border: 1px solid var(--color-separator);
  border-radius: var(--radius-sm);
  padding: var(--space-2);
}
.report__actions { display: flex; justify-content: flex-end; gap: var(--space-2); }
</style>
