<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { operationsAdminApi as api, type DetailedHealth } from "../../infrastructure/operationsAdminApi";
import { t } from '@/shared/i18n'
const health = ref<DetailedHealth>();
const versions = ref<string[]>([]);
const healthLoading = ref(true);
const nativeLoading = ref(true);
const healthError = ref("");
const nativeError = ref("");
const serviceNames: Record<string, string> = { database: t('admin.SystemAdminPage.s16'), storage: t('admin.SystemAdminPage.s17') };
const healthNames: Record<string, string> = { healthy: t('admin.SystemAdminPage.s18'), degraded: t('admin.SystemAdminPage.s19'), unhealthy: t('admin.SystemAdminPage.s20') };
async function loadHealth() {
    healthLoading.value = true;
    healthError.value = "";
    try { const data = await api.health(); if (alive) health.value = data; }
    catch { if (alive) healthError.value = t('admin.SystemAdminPage.s21'); }
    finally { if (alive) healthLoading.value = false; }
}
async function loadNative() {
    nativeLoading.value = true;
    nativeError.value = "";
    try {
        const data = await api.matrixVersions();
        if (!data.versions?.length) throw new Error("Missing Matrix versions");
        if (alive) versions.value = data.versions;
    }
    catch { if (alive) nativeError.value = t('admin.SystemAdminPage.s22'); }
    finally { if (alive) nativeLoading.value = false; }
}
let alive = true;
onMounted(() => { void loadHealth(); void loadNative(); });
onUnmounted(() => {
    alive = false;
});
</script>
<template>
  <div class="admin-page">
    <header>
      <h1>{{ $t('admin.SystemAdminPage.s1') }}</h1>
      <p>{{ $t('admin.SystemAdminPage.s2') }}</p>
    </header>
    <section class="admin-card" :aria-label="$t('admin.SystemAdminPage.s3')">
      <div class="admin-card__header"><h2>{{ $t('admin.SystemAdminPage.s3') }}</h2><button class="admin-button" :disabled="nativeLoading" @click="loadNative">{{ $t('admin.SystemAdminPage.s4') }}</button></div>
      <p v-if="nativeLoading" role="status">{{ $t('admin.SystemAdminPage.s5') }}</p>
      <p v-else-if="nativeError" role="alert" class="admin-error">{{ nativeError }}</p>
      <template v-else><p role="status">{{ $t('admin.SystemAdminPage.s6') }}</p><p>{{ $t('admin.SystemAdminPage.s7', [versions.join('、')]) }}</p></template>
      <p>{{ $t('admin.SystemAdminPage.s8') }}</p>
    </section>
    <section class="admin-card" :aria-label="$t('admin.SystemAdminPage.s9')">
      <div class="admin-card__header"><h2>{{ $t('admin.SystemAdminPage.s9') }}</h2><button class="admin-button" :disabled="healthLoading" @click="loadHealth">{{ $t('admin.SystemAdminPage.s10') }}</button></div>
      <p v-if="healthLoading" role="status">{{ $t('admin.SystemAdminPage.s11') }}</p>
      <p v-else-if="healthError" role="alert" class="admin-error">{{ healthError }}</p>
      <template v-else-if="health">
        <p>{{ $t('admin.SystemAdminPage.s13', [healthNames[health.status] || $t('admin.SystemAdminPage.s12'), health.uptime]) }}</p>
        <dl><template v-for="(label, key) in serviceNames" :key="key"><dt>{{ label }}</dt><dd>{{ healthNames[health.services?.[key]?.status || ''] || $t('admin.SystemAdminPage.s14') }}<span v-if="health.services?.[key]?.latency"> · {{ health.services[key]!.latency }}</span></dd></template></dl>
        <p>{{ $t('admin.SystemAdminPage.s15', [health.timestamp]) }}</p>
      </template>
    </section>
  </div>
</template>

