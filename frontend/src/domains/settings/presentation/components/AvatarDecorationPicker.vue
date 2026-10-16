<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { communityAssetsApi, type AvatarDecoration, type AvatarDecorationChoice } from '@/domains/admin/infrastructure/communityAssetsApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import type { AvatarDecorationPlacement } from '@/shared/types/mastodon'
import { useSettingsScope } from '../composables/useSettingsScope'

import { t } from '@/shared/i18n'
const auth = useAuthStore()
const items = ref<AvatarDecoration[]>([]), selected = ref<AvatarDecorationChoice[]>([]), limit = ref(1), active = ref(0)
const busy = ref(false), loading = ref(true), loaded = ref(false), error = ref(''), notice = ref('')
const scope = useSettingsScope(() => { items.value = []; loaded.value = false; selected.value = []; busy.value = false; loading.value = false; error.value = ''; notice.value = '' })

const imageUrl = (id: string) => `/api/v1/avatar_decorations/${encodeURIComponent(id)}/image`
const names = computed(() => new Map(items.value.map(i => [i.id, i.name])))
const preview = computed<AvatarDecorationPlacement[]>(() => selected.value.map(d => ({ ...d, url: imageUrl(d.id) })))
const current = computed(() => selected.value[active.value])
const full = computed(() => selected.value.length >= limit.value)

function fromAccount(): AvatarDecorationChoice[] {
  const user = auth.currentUser
  if (!user) return []
  if (user.avatar_decorations?.length) return user.avatar_decorations.map(({ id, angle, flip_h, offset_x, offset_y }) => ({ id, angle: angle ?? 0, flip_h: !!flip_h, offset_x: offset_x ?? 0, offset_y: offset_y ?? 0 }))
  return user.avatar_decoration_id && user.avatar_decoration_id !== '0' ? [{ id: user.avatar_decoration_id, angle: 0, flip_h: false, offset_x: 0, offset_y: 0 }] : []
}
async function load() {
  const done = scope()
  loading.value = true; error.value = ''
  selected.value = fromAccount(); active.value = 0
  try {
    const [list, settings] = await Promise.all([communityAssetsApi.decorations(), communityAssetsApi.decorationSettings().catch(() => ({ limit: 1, max_limit: 16 }))])
    if (!done()) return
    items.value = list
    limit.value = Math.max(1, settings.limit)
    const available = new Set(list.map(i => i.id))
    selected.value = selected.value.filter(d => available.has(d.id))
    loaded.value = true
  } catch { if (done()) error.value = t('settings.AvatarDecorationPicker.s22') }
  finally { if (done()) loading.value = false }
}
function add(item: AvatarDecoration) {
  notice.value = ''
  const placed: AvatarDecorationChoice = { id: item.id, angle: 0, flip_h: false, offset_x: 0, offset_y: 0 }
  if (limit.value === 1) { selected.value = [placed]; active.value = 0; return }
  if (full.value) { error.value = t('settings.AvatarDecorationPicker.s23', [limit.value]); return }
  error.value = ''
  selected.value = [...selected.value, placed]
  active.value = selected.value.length - 1
}
function remove(index: number) {
  selected.value = selected.value.filter((_, i) => i !== index)
  active.value = Math.min(active.value, Math.max(0, selected.value.length - 1))
  error.value = ''
}
function update(patch: Partial<AvatarDecorationChoice>) {
  const i = active.value
  if (!selected.value[i]) return
  selected.value = selected.value.map((d, j) => j === i ? { ...d, ...patch } : d)
}
function num(event: Event, min: number, max: number): number {
  const v = Number((event.target as HTMLInputElement).value)
  return Number.isFinite(v) ? Math.max(min, Math.min(max, v)) : 0
}
async function save() {
  if (busy.value || loading.value || !loaded.value) return
  const done = scope()
  busy.value = true; error.value = ''; notice.value = ''
  try {
    await communityAssetsApi.selectDecorations(selected.value)
    if (!done()) return
    await auth.fetchCurrentUser()
    if (done()) notice.value = t('settings.AvatarDecorationPicker.s24')
  } catch { if (done()) error.value = t('settings.AvatarDecorationPicker.s25') }
  finally { if (done()) busy.value = false }
}
onMounted(load)
</script>

<template>
  <form class="decoration-picker" @submit.prevent="save">
    <p class="decoration-picker__hint">{{ $t('settings.AvatarDecorationPicker.s1', [limit]) }}</p>
    <p v-if="loading" role="status">{{ $t('settings.AvatarDecorationPicker.s2') }}</p>
    <template v-else>
      <div class="decoration-picker__layout">
        <div class="decoration-picker__preview" :aria-label="$t('settings.AvatarDecorationPicker.s3')">
          <UserAvatar v-if="auth.currentUser" :account="auth.currentUser" :size="96" :decorations="preview" />
        </div>
        <div class="decoration-picker__editor">
          <ul v-if="selected.length" class="decoration-picker__selected" :aria-label="$t('settings.AvatarDecorationPicker.s4')">
            <li v-for="(d, i) in selected" :key="`${i}:${d.id}`" :class="{ 'is-active': i === active }">
              <button type="button" class="decoration-picker__chip" :aria-pressed="i === active" @click="active = i">
                <img :src="imageUrl(d.id)" alt="" width="28" height="28" />{{ names.get(d.id) || d.id }}
              </button>
              <button type="button" class="btn btn--ghost btn--sm" :disabled="busy" :aria-label="$t('settings.AvatarDecorationPicker.s5', [names.get(d.id) || d.id])" @click="remove(i)">{{ $t('settings.AvatarDecorationPicker.s6') }}</button>
            </li>
          </ul>
          <p v-else class="decoration-picker__empty">{{ $t('settings.AvatarDecorationPicker.s7') }}</p>
          <fieldset v-if="current" class="decoration-picker__controls" :disabled="busy">
            <legend>{{ $t('settings.AvatarDecorationPicker.s8', [names.get(current.id) || current.id]) }}</legend>
            <label>{{ $t('settings.AvatarDecorationPicker.s9') }}
              <input type="range" min="-0.5" max="0.5" step="0.01" :value="current.angle" @input="update({ angle: num($event, -0.5, 0.5) })" />
              <input class="decoration-picker__num" type="number" inputmode="numeric" min="-180" max="180" step="1" :value="Math.round((current.angle ?? 0) * 360)" :aria-label="$t('settings.AvatarDecorationPicker.s10')" @change="update({ angle: num($event, -180, 180) / 360 })" />°
            </label>
            <label>{{ $t('settings.AvatarDecorationPicker.s11') }}
              <input type="range" min="-0.25" max="0.25" step="0.01" :value="current.offset_x" @input="update({ offset_x: num($event, -0.25, 0.25) })" />
              <input class="decoration-picker__num" type="number" inputmode="numeric" min="-25" max="25" step="1" :value="Math.round((current.offset_x ?? 0) * 100)" :aria-label="$t('settings.AvatarDecorationPicker.s12')" @change="update({ offset_x: num($event, -25, 25) / 100 })" />%
            </label>
            <label>{{ $t('settings.AvatarDecorationPicker.s13') }}
              <input type="range" min="-0.25" max="0.25" step="0.01" :value="current.offset_y" @input="update({ offset_y: num($event, -0.25, 0.25) })" />
              <input class="decoration-picker__num" type="number" inputmode="numeric" min="-25" max="25" step="1" :value="Math.round((current.offset_y ?? 0) * 100)" :aria-label="$t('settings.AvatarDecorationPicker.s14')" @change="update({ offset_y: num($event, -25, 25) / 100 })" />%
            </label>
            <label class="decoration-picker__flip"><input type="checkbox" :checked="current.flip_h" @change="update({ flip_h: ($event.target as HTMLInputElement).checked })" />{{ $t('settings.AvatarDecorationPicker.s15') }}</label>
            <button type="button" class="btn btn--ghost btn--sm" @click="update({ angle: 0, flip_h: false, offset_x: 0, offset_y: 0 })">{{ $t('settings.AvatarDecorationPicker.s16') }}</button>
          </fieldset>
        </div>
      </div>
      <div class="decoration-options" role="list" :aria-label="$t('settings.AvatarDecorationPicker.s17')">
        <button
          v-for="item in items"
          :key="item.id"
          type="button"
          role="listitem"
          class="decoration-option"
          :disabled="busy || (full && limit > 1)"
          @click="add(item)"
        >
          <img :src="item.image_url" alt="" width="56" height="56" /><span>{{ item.name }}</span>
        </button>
        <p v-if="!items.length">{{ $t('settings.AvatarDecorationPicker.s18') }}</p>
      </div>
    </template>
    <p v-if="error" role="alert">{{ error }} <button v-if="!loaded" type="button" class="btn btn--ghost btn--sm" :disabled="busy || loading" @click="load">{{ $t('settings.AvatarDecorationPicker.s19') }}</button></p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <div class="settings-actions"><button type="submit" class="btn btn--primary" :disabled="busy || loading || !loaded">{{ busy ? $t('settings.AvatarDecorationPicker.s20') : $t('settings.AvatarDecorationPicker.s21') }}</button></div>
  </form>
</template>

<style scoped>
.decoration-picker { padding: 20px; }
.decoration-picker__hint { margin: 0 0 12px; color: var(--color-label-secondary); }
.decoration-picker__layout { display: flex; flex-wrap: wrap; gap: 24px; align-items: flex-start; }
.decoration-picker__preview { flex: 0 0 auto; display: flex; align-items: center; justify-content: center; width: 192px; height: 192px; border-radius: 12px; background: var(--color-fill); overflow: hidden; }
.decoration-picker__editor { flex: 1 1 240px; min-width: 0; }
.decoration-picker__selected { list-style: none; margin: 0 0 12px; padding: 0; display: flex; flex-direction: column; gap: 6px; }
.decoration-picker__selected li { display: flex; align-items: center; gap: 8px; }
.decoration-picker__chip { display: inline-flex; align-items: center; gap: 8px; flex: 1; min-width: 0; padding: 6px 10px; border: 1px solid var(--color-separator); border-radius: 8px; background: none; color: inherit; text-align: left; cursor: pointer; }
.is-active .decoration-picker__chip { border-color: var(--color-accent, #4c8bf5); }
.decoration-picker__chip img { object-fit: contain; }
.decoration-picker__controls { border: 1px solid var(--color-separator); border-radius: 8px; padding: 10px 12px; display: flex; flex-direction: column; gap: 8px; min-width: 0; }
.decoration-picker__controls label { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.decoration-picker__controls input[type="range"] { flex: 1 1 120px; min-width: 0; }
.decoration-picker__num { width: 4.5em; }
.decoration-picker__empty { color: var(--color-label-secondary); }
.decoration-options { display: grid; grid-template-columns: repeat(auto-fill, minmax(96px, 1fr)); gap: 12px; margin: 16px 0; }
.decoration-option { display: flex; flex-direction: column; align-items: center; gap: 6px; padding: 8px; border: 1px solid var(--color-separator); border-radius: 8px; background: none; color: inherit; cursor: pointer; }
.decoration-option:disabled { opacity: .5; cursor: not-allowed; }
.decoration-option img { object-fit: contain; }
.decoration-option span { font-size: 13px; overflow-wrap: anywhere; text-align: center; }
</style>
