<script setup lang="ts">
import { AdminAlert, AdminBadge, AdminButton, AdminEmpty, AdminField, AdminPageHeader, AdminRow } from "../components/ui"
import { onMounted, onBeforeUnmount, reactive, ref } from 'vue'
import { accessAdminApi as api, type AdminInvite } from '../../infrastructure/accessAdminApi'
import { t } from '@/shared/i18n'
const rows = ref<AdminInvite[]>([]), total = ref(0), offset = ref(0), state = ref('')
const error = ref(''), notice = ref(''), loading = ref(false), busy = ref(false)
const draft = reactive({ count: 1, max_uses: 1, expires_in: 604800 })
let alive = true, version = 0
onBeforeUnmount(() => { alive = false; version++ })
async function load(reset = false) { if (reset) offset.value = 0; const request = ++version; loading.value = true; error.value = ''; try { const result = await api.invites(state.value, offset.value); if (alive && request === version) { rows.value = result.items; total.value = result.total } } catch { if (alive && request === version) error.value = t('admin.InvitesAdminPage.s36') } finally { if (alive && request === version) loading.value = false } }
async function create() { if (busy.value) return; busy.value = true; error.value = ''; notice.value = ''; try { const result = await api.createInvites({ ...draft }); if (!alive) return; notice.value = t('admin.InvitesAdminPage.s37', [result.items.length]); state.value = ''; await load(true) } catch { if (alive) error.value = t('admin.InvitesAdminPage.s38') } finally { if (alive) busy.value = false } }
async function toggle(invite: AdminInvite) { if (!window.confirm(invite.disabled ? t('admin.InvitesAdminPage.s39') : t('admin.InvitesAdminPage.s40'))) return; busy.value = true; error.value = ''; try { await api.setInviteDisabled(invite.id, !invite.disabled); if (alive) await load() } catch { if (alive) error.value = t('admin.InvitesAdminPage.s41') } finally { if (alive) busy.value = false } }
async function copy(code: string) { try { await navigator.clipboard.writeText(code); if (alive) notice.value = t('admin.InvitesAdminPage.s42') } catch { if (alive) error.value = t('admin.InvitesAdminPage.s43') } }
function status(invite: AdminInvite) { if (invite.disabled) return t('admin.InvitesAdminPage.s19'); if (invite.expires_at && Date.parse(invite.expires_at) <= Date.now()) return t('admin.InvitesAdminPage.s20'); if (invite.max_uses > 0 && invite.uses >= invite.max_uses) return t('admin.InvitesAdminPage.s44'); return invite.available ? t('admin.InvitesAdminPage.s18') : t('admin.InvitesAdminPage.s45') }
function page(delta: number) { offset.value = Math.max(0, offset.value + delta * 40); void load() }
onMounted(() => load())
</script>

<template>
  <section class="admin-page">
    <AdminPageHeader :title="$t('admin.InvitesAdminPage.s1')" :description="$t('admin.InvitesAdminPage.s2')">
      <AdminButton size="sm" :disabled="busy || loading" @click="load()">{{ $t('admin.InvitesAdminPage.s3') }}</AdminButton>
    </AdminPageHeader>
    <AdminAlert v-if="error" tone="danger">{{ error }}</AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <form class="admin-card" :aria-label="$t('admin.InvitesAdminPage.s4')" @submit.prevent="create">
      <h2>{{ $t('admin.InvitesAdminPage.s5') }}</h2>
      <div class="admin-toolbar">
        <AdminField :label="$t('admin.InvitesAdminPage.s6')"><template #default="{ id }"><input :id="id" v-model.number="draft.count" type="number" min="1" max="100" required></template></AdminField>
        <AdminField :label="$t('admin.InvitesAdminPage.s7')" :hint="$t('admin.InvitesAdminPage.s8')"><template #default="{ id }"><input :id="id" v-model.number="draft.max_uses" type="number" min="0" max="10000" required></template></AdminField>
        <AdminField :label="$t('admin.InvitesAdminPage.s9')"><template #default="{ id }"><select :id="id" v-model.number="draft.expires_in"><option :value="3600">{{ $t('admin.InvitesAdminPage.s10') }}</option><option :value="86400">{{ $t('admin.InvitesAdminPage.s11') }}</option><option :value="604800">{{ $t('admin.InvitesAdminPage.s12') }}</option><option :value="2592000">{{ $t('admin.InvitesAdminPage.s13') }}</option><option :value="0">{{ $t('admin.InvitesAdminPage.s14') }}</option></select></template></AdminField>
      </div>
      <div class="admin-form-actions"><AdminButton type="submit" variant="primary" :disabled="busy || loading">{{ $t('admin.InvitesAdminPage.s4') }}</AdminButton></div>
    </form>
    <form class="admin-toolbar" :aria-label="$t('admin.InvitesAdminPage.s15')" @submit.prevent="load(true)">
      <AdminField :label="$t('admin.InvitesAdminPage.s16')"><template #default="{ id }"><select :id="id" v-model="state"><option value="">{{ $t('admin.InvitesAdminPage.s17') }}</option><option value="active">{{ $t('admin.InvitesAdminPage.s18') }}</option><option value="disabled">{{ $t('admin.InvitesAdminPage.s19') }}</option><option value="expired">{{ $t('admin.InvitesAdminPage.s20') }}</option><option value="exhausted">{{ $t('admin.InvitesAdminPage.s21') }}</option></select></template></AdminField>
      <AdminButton type="submit" :disabled="busy || loading">{{ $t('admin.InvitesAdminPage.s22') }}</AdminButton>
    </form>
    <section class="admin-card" :aria-busy="loading">
      <p v-if="loading" class="admin-empty" role="status">{{ $t('admin.InvitesAdminPage.s23') }}</p>
      <AdminEmpty v-else-if="!error && !rows.length">{{ $t('admin.InvitesAdminPage.s24') }}</AdminEmpty>
      <ul v-else class="admin-list">
        <AdminRow v-for="invite in rows" :key="invite.id" :title="invite.code" mono>
          <template #meta>
            <AdminBadge :tone="status(invite) === $t('admin.InvitesAdminPage.s18') ? 'success' : 'neutral'">{{ status(invite) }}</AdminBadge>
            <span>{{ $t('admin.InvitesAdminPage.s26', [invite.uses, invite.max_uses === 0 ? $t('admin.InvitesAdminPage.s25') : invite.max_uses]) }}</span>
            <span>{{ $t('admin.InvitesAdminPage.s27', [invite.expires_at ? new Date(invite.expires_at).toLocaleString() : $t('admin.InvitesAdminPage.s14')]) }}</span>
            <span>{{ $t('admin.InvitesAdminPage.s28', [invite.account_id]) }}</span>
          </template>
          <AdminButton size="sm" @click="copy(invite.code)">{{ $t('admin.InvitesAdminPage.s29') }}</AdminButton>
          <AdminButton size="sm" :variant="invite.disabled ? 'secondary' : 'danger'" :disabled="busy" @click="toggle(invite)">{{ invite.disabled ? $t('admin.InvitesAdminPage.s30') : $t('admin.InvitesAdminPage.s31') }}</AdminButton>
        </AdminRow>
      </ul>
    </section>
    <nav class="admin-pagination" :aria-label="$t('admin.InvitesAdminPage.s32')">
      <AdminButton size="sm" :disabled="offset === 0 || busy || loading" @click="page(-1)">{{ $t('admin.InvitesAdminPage.s33') }}</AdminButton>
      <span>{{ $t('admin.InvitesAdminPage.s34', [total]) }}</span>
      <AdminButton size="sm" :disabled="offset + 40 >= total || busy || loading" @click="page(1)">{{ $t('admin.InvitesAdminPage.s35') }}</AdminButton>
    </nav>
  </section>
</template>
