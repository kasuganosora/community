<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { AdminAlert, AdminBadge, AdminButton, AdminEmpty, AdminInlineForm, AdminPageHeader, AdminRow } from "../components/ui";
import { operationsAdminApi as api, type Relay } from "../../infrastructure/operationsAdminApi";
import { t } from '@/shared/i18n'
const items = ref<Relay[]>([]);
const loading = ref(true);
const busy = ref(false);
const error = ref("");
const notice = ref("");
const inbox = ref("");
let alive = true;
const states: Record<string, string> = { idle: t('admin.FederationAdminPage.s14'), pending: t('admin.FederationAdminPage.s15'), accepted: t('admin.FederationAdminPage.s16'), rejected: t('admin.FederationAdminPage.s17') };
const tones: Record<string, "neutral" | "success" | "warning" | "danger"> = { idle: "neutral", pending: "warning", accepted: "success", rejected: "danger" };
const active = (relay: Relay) => ["pending", "accepted"].includes(relay.state);
async function load() {
    loading.value = true;
    error.value = "";
    try {
        const data = await api.relays();
        if (alive)
            items.value = data;
    }
    catch {
        if (alive)
            error.value = t('admin.FederationAdminPage.s18');
    }
    finally {
        if (alive)
            loading.value = false;
    }
}
async function add() {
    if (busy.value)
        return;
    busy.value = true;
    error.value = "";
    notice.value = "";
    try {
        await api.addRelay(inbox.value.trim());
        inbox.value = "";
        notice.value = t('admin.FederationAdminPage.s19');
        await load();
    }
    catch {
        error.value = t('admin.FederationAdminPage.s20');
    }
    finally {
        busy.value = false;
    }
}
async function act(relay: Relay, action: "enable" | "disable" | "delete") {
    const label = action === "delete" ? t('admin.FederationAdminPage.s13') : action === "disable" ? t('admin.FederationAdminPage.s11') : t('admin.FederationAdminPage.s12');
    if (busy.value || !window.confirm(t('admin.FederationAdminPage.s23', [label, relay.inbox_url, action === "delete" ? t('admin.FederationAdminPage.s21') : t('admin.FederationAdminPage.s22')])))
        return;
    busy.value = true;
    error.value = "";
    notice.value = "";
    try {
        await api.relayAction(relay.id, action);
        notice.value = t('admin.FederationAdminPage.s24', [label]);
        await load();
    }
    catch {
        error.value = t('admin.FederationAdminPage.s25', [label]);
    }
    finally {
        busy.value = false;
    }
}
onMounted(load);
onUnmounted(() => {
    alive = false;
});
</script>
<template>
  <div class="admin-page">
    <AdminPageHeader :title="$t('admin.FederationAdminPage.s1')" :description="$t('admin.FederationAdminPage.s2')" />
    <AdminAlert v-if="error" tone="danger">
      {{ error }}
      <template #action><AdminButton size="sm" :disabled="busy || loading" @click="load">{{ $t('admin.FederationAdminPage.s3') }}</AdminButton></template>
    </AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <section class="admin-card" :aria-label="$t('admin.FederationAdminPage.s4')">
      <AdminInlineForm v-model="inbox" :label="$t('admin.FederationAdminPage.s5')" type="url" placeholder="https://relay.example/inbox" :button-label="$t('admin.FederationAdminPage.s4')" :busy="busy" :disabled="loading" @submit="add" />
    </section>
    <section class="admin-card" :aria-label="$t('admin.FederationAdminPage.s6')" :aria-busy="loading">
      <div class="admin-card__header">
        <h2>{{ $t('admin.FederationAdminPage.s7') }}</h2>
        <AdminButton size="sm" :disabled="busy || loading" @click="load">{{ $t('admin.FederationAdminPage.s8') }}</AdminButton>
      </div>
      <p v-if="loading" class="admin-empty" role="status">{{ $t('admin.FederationAdminPage.s9') }}</p>
      <AdminEmpty v-else-if="!items.length && !error">{{ $t('admin.FederationAdminPage.s10') }}</AdminEmpty>
      <ul v-else class="admin-list">
        <AdminRow v-for="relay in items" :key="relay.id" :title="relay.inbox_url" mono>
          <template #meta><AdminBadge :tone="tones[relay.state] || 'neutral'">{{ states[relay.state] || relay.state }}</AdminBadge></template>
          <AdminButton size="sm" :disabled="busy" @click="act(relay, active(relay) ? 'disable' : 'enable')">{{ active(relay) ? $t('admin.FederationAdminPage.s11') : $t('admin.FederationAdminPage.s12') }}</AdminButton>
          <AdminButton size="sm" variant="danger" :disabled="busy" @click="act(relay, 'delete')">{{ $t('admin.FederationAdminPage.s13') }}</AdminButton>
        </AdminRow>
      </ul>
    </section>
  </div>
</template>
