<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { AdminAlert, AdminButton, AdminCheck, AdminEmpty, AdminField, AdminPageHeader, AdminRow } from "../components/ui";
import { operationsAdminApi as api, type CaptchaConfig, type SiteRule } from "../../infrastructure/operationsAdminApi";
import { t } from '@/shared/i18n'
import ApiTokenPolicyCard from '../components/ApiTokenPolicyCard.vue'
const props = withDefaults(defineProps<{ mode?: 'rules' | 'security' | 'all' }>(), { mode: 'all' })
const rules = ref<SiteRule[]>([]);
const config = ref<CaptchaConfig>();
const saved = ref("");
const secret = ref("");
const loading = ref(true);
const busy = ref(false);
const error = ref("");
const notice = ref("");
const editing = ref<string | null>(null);
const text = ref("");
const dirty = computed(() => !!config.value && (JSON.stringify(config.value) !== saved.value || !!secret.value));
let alive = true;
async function load() {
    loading.value = true;
    error.value = "";
    try {
        const [r, c] = await Promise.all([props.mode === 'security' ? Promise.resolve([]) : api.rules(), props.mode === 'rules' ? Promise.resolve(undefined) : api.captcha()]);
        if (alive) {
            rules.value = r;
            config.value = c;
            saved.value = JSON.stringify(c) || "";
            secret.value = "";
        }
    }
    catch {
        if (alive)
            error.value = t('admin.SettingsAdminPage.s32');
    }
    finally {
        if (alive)
            loading.value = false;
    }
}
async function saveCaptcha() {
    if (busy.value || !config.value || config.value.environment_override)
        return;
    busy.value = true;
    error.value = "";
    notice.value = "";
    try {
        await api.saveCaptcha(config.value, secret.value);
        secret.value = "";
        saved.value = JSON.stringify(config.value);
        notice.value = t('admin.SettingsAdminPage.s33');
    }
    catch {
        error.value = t('admin.SettingsAdminPage.s34');
    }
    finally {
        busy.value = false;
    }
}
async function saveRule() {
    if (busy.value)
        return;
    busy.value = true;
    error.value = "";
    notice.value = "";
    try {
        const r = await api.saveRule(editing.value, text.value.trim());
        const i = rules.value.findIndex(x => x.id === r.id);
        if (i >= 0)
            rules.value[i] = r;
        else
            rules.value.push(r);
        editing.value = null;
        text.value = "";
        notice.value = t('admin.SettingsAdminPage.s35');
    }
    catch {
        error.value = t('admin.SettingsAdminPage.s36');
    }
    finally {
        busy.value = false;
    }
}
async function remove(rule: SiteRule) {
    if (busy.value || !window.confirm(t('admin.SettingsAdminPage.s37', [rule.text])))
        return;
    busy.value = true;
    error.value = "";
    notice.value = "";
    try {
        await api.deleteRule(rule.id);
        rules.value = rules.value.filter(r => r.id !== rule.id);
        if (editing.value === rule.id) {
            editing.value = null;
            text.value = "";
        }
        notice.value = t('admin.SettingsAdminPage.s38');
    }
    catch {
        error.value = t('admin.SettingsAdminPage.s39');
    }
    finally {
        busy.value = false;
    }
}
onMounted(load);
onUnmounted(() => {
    alive = false;
    secret.value = "";
});
</script>
<template>
  <div class="admin-page">
    <AdminPageHeader :title="mode === 'rules' ? $t('admin.SettingsAdminPage.s1') : mode === 'security' ? $t('admin.SettingsAdminPage.s2') : $t('admin.SettingsAdminPage.s3')" :description="mode === 'rules' ? $t('admin.SettingsAdminPage.s4') : mode === 'security' ? $t('admin.SettingsAdminPage.s5') : $t('admin.SettingsAdminPage.s6')" />
    <p v-if="loading" class="admin-empty" role="status">{{ $t('admin.SettingsAdminPage.s7') }}</p>
    <AdminAlert v-if="error" tone="danger">
      {{ error }}
      <template v-if="!config" #action><AdminButton size="sm" :disabled="loading" @click="load">{{ $t('admin.SettingsAdminPage.s8') }}</AdminButton></template>
    </AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <template v-if="!loading && (config || mode === 'rules')">
      <section v-if="mode !== 'security'" class="admin-card">
        <h2>{{ $t('admin.SettingsAdminPage.s1') }}</h2>
        <p>{{ $t('admin.SettingsAdminPage.s9') }}</p>
        <AdminEmpty v-if="!rules.length">{{ $t('admin.SettingsAdminPage.s10') }}</AdminEmpty>
        <ul v-else class="admin-list">
          <AdminRow v-for="rule in rules" :key="rule.id" :title="rule.text">
            <AdminButton size="sm" :disabled="busy" @click="editing = rule.id; text = rule.text">{{ $t('admin.SettingsAdminPage.s11') }}</AdminButton>
            <AdminButton size="sm" variant="danger" :disabled="busy" @click="remove(rule)">{{ $t('admin.SettingsAdminPage.s12') }}</AdminButton>
          </AdminRow>
        </ul>
        <form class="admin-form" @submit.prevent="saveRule">
          <AdminField :label="editing ? $t('admin.SettingsAdminPage.s13') : $t('admin.SettingsAdminPage.s14')"><template #default="{ id }"><textarea :id="id" v-model="text" required maxlength="255" :disabled="busy" /></template></AdminField>
          <div class="admin-form-actions">
            <AdminButton type="submit" variant="primary" :loading="busy" :disabled="!text.trim()">{{ busy ? $t('admin.SettingsAdminPage.s15') : editing ? $t('admin.SettingsAdminPage.s16') : $t('admin.SettingsAdminPage.s17') }}</AdminButton>
            <AdminButton v-if="editing" :disabled="busy" @click="editing = null; text = ''">{{ $t('admin.SettingsAdminPage.s18') }}</AdminButton>
          </div>
        </form>
      </section>
      <form v-if="config && mode !== 'rules'" class="admin-card admin-preferences" @submit.prevent="saveCaptcha">
        <div class="admin-preferences__heading"><h2>{{ $t('admin.SettingsAdminPage.s19') }}</h2></div>
        <AdminAlert v-if="config.environment_override" tone="info">{{ $t('admin.SettingsAdminPage.s20') }}</AdminAlert>
        <AdminAlert v-else-if="config.enabled && config.effective_enabled === false" tone="warning">{{ $t('admin.SettingsAdminPage.s21') }}</AdminAlert>
        <div class="admin-form admin-preferences__fields">
          <AdminCheck variant="switch" class="admin-preferences__toggle" v-model="config.enabled" :label="$t('admin.SettingsAdminPage.s22')" :disabled="busy || config.environment_override" />
          <AdminField :label="$t('admin.SettingsAdminPage.s23')">
            <template #default="{ id }">
              <select :id="id" v-model="config.provider" :disabled="busy || config.environment_override">
                <option value="none">{{ $t('admin.SettingsAdminPage.s24') }}</option>
                <option value="local">{{ $t('admin.SettingsAdminPage.s25') }}</option>
                <option value="recaptcha">Google reCAPTCHA</option>
                <option value="cloudflare">Cloudflare Turnstile</option>
                <option value="hcaptcha">hCaptcha</option>
              </select>
            </template>
          </AdminField>
          <AdminField :label="$t('admin.SettingsAdminPage.s26')"><template #default="{ id }"><input :id="id" v-model="config.site_key" :disabled="busy || config.environment_override" autocomplete="off" /></template></AdminField>
          <AdminField :label="$t('admin.SettingsAdminPage.s27')" :hint="$t('admin.SettingsAdminPage.s28')"><template #default="{ id }"><input :id="id" v-model="secret" type="password" autocomplete="new-password" :disabled="busy || config.environment_override" :placeholder="$t('admin.SettingsAdminPage.s29')" /></template></AdminField>
        </div>
        <div class="admin-form-actions"><AdminButton type="submit" variant="primary" :loading="busy" :disabled="!dirty || config.environment_override">{{ busy ? $t('admin.SettingsAdminPage.s30') : $t('admin.SettingsAdminPage.s31') }}</AdminButton></div>
      </form>
    </template>
    <ApiTokenPolicyCard v-if="mode === 'security'" />
  </div>
</template>
