<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { AdminAlert, AdminButton, AdminCheck, AdminEmpty, AdminField, AdminPageHeader, AdminRow } from "../components/ui";
import { operationsAdminApi as api, type CaptchaConfig, type SiteRule } from "../../infrastructure/operationsAdminApi";
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
            error.value = "无法加载站点设置，请重试。";
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
        notice.value = "验证码设置已保存。";
    }
    catch {
        error.value = "验证码设置保存失败，请检查配置后重试。";
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
        notice.value = "站点规则已保存。";
    }
    catch {
        error.value = "规则保存失败，输入内容已保留，请重试。";
    }
    finally {
        busy.value = false;
    }
}
async function remove(rule: SiteRule) {
    if (busy.value || !window.confirm(`确认删除规则“${rule.text}”？`))
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
        notice.value = "规则已删除。";
    }
    catch {
        error.value = "删除失败，请重试。";
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
    <AdminPageHeader :title="mode === 'rules' ? '社区规则' : mode === 'security' ? '安全' : '站点设置'" :description="mode === 'rules' ? '维护向社区成员公开的站点规则。' : mode === 'security' ? '配置注册与登录验证码。' : '维护公开的社区规则及注册验证码。'" />
    <p v-if="loading" class="admin-empty" role="status">正在加载设置…</p>
    <AdminAlert v-if="error" tone="danger">
      {{ error }}
      <template v-if="!config" #action><AdminButton size="sm" :disabled="loading" @click="load">重试加载</AdminButton></template>
    </AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <template v-if="!loading && (config || mode === 'rules')">
      <section v-if="mode !== 'security'" class="admin-card">
        <h2>社区规则</h2>
        <p>规则会展示在站点介绍及注册页面。</p>
        <AdminEmpty v-if="!rules.length">尚未设置规则。</AdminEmpty>
        <ul v-else class="admin-list">
          <AdminRow v-for="rule in rules" :key="rule.id" :title="rule.text">
            <AdminButton size="sm" :disabled="busy" @click="editing = rule.id; text = rule.text">编辑规则</AdminButton>
            <AdminButton size="sm" variant="danger" :disabled="busy" @click="remove(rule)">删除规则</AdminButton>
          </AdminRow>
        </ul>
        <form class="admin-form" @submit.prevent="saveRule">
          <AdminField :label="editing ? '编辑规则内容' : '新规则内容'"><template #default="{ id }"><textarea :id="id" v-model="text" required maxlength="255" :disabled="busy" /></template></AdminField>
          <div class="admin-form-actions">
            <AdminButton type="submit" variant="primary" :loading="busy" :disabled="!text.trim()">{{ busy ? '处理中…' : editing ? '保存规则' : '添加规则' }}</AdminButton>
            <AdminButton v-if="editing" :disabled="busy" @click="editing = null; text = ''">取消编辑</AdminButton>
          </div>
        </form>
      </section>
      <form v-if="config && mode !== 'rules'" class="admin-card admin-preferences" @submit.prevent="saveCaptcha">
        <div class="admin-preferences__heading"><h2>验证码</h2></div>
        <AdminAlert v-if="config.environment_override" tone="info">验证码由服务器环境配置，此处仅供查看。</AdminAlert>
        <AdminAlert v-else-if="config.enabled && config.effective_enabled === false" tone="warning">已启用但尚未生效，请检查供应商和密钥配置。</AdminAlert>
        <div class="admin-form admin-preferences__fields">
          <AdminCheck variant="switch" class="admin-preferences__toggle" v-model="config.enabled" label="启用注册验证码" :disabled="busy || config.environment_override" />
          <AdminField label="验证码服务">
            <template #default="{ id }">
              <select :id="id" v-model="config.provider" :disabled="busy || config.environment_override">
                <option value="none">无</option>
                <option value="local">本站图片验证码</option>
                <option value="recaptcha">Google reCAPTCHA</option>
                <option value="cloudflare">Cloudflare Turnstile</option>
                <option value="hcaptcha">hCaptcha</option>
              </select>
            </template>
          </AdminField>
          <AdminField label="站点公钥"><template #default="{ id }"><input :id="id" v-model="config.site_key" :disabled="busy || config.environment_override" autocomplete="off" /></template></AdminField>
          <AdminField label="替换服务密钥" hint="现有密钥不会回显，仅在需要替换时填写。"><template #default="{ id }"><input :id="id" v-model="secret" type="password" autocomplete="new-password" :disabled="busy || config.environment_override" placeholder="留空保留现有密钥" /></template></AdminField>
        </div>
        <div class="admin-form-actions"><AdminButton type="submit" variant="primary" :loading="busy" :disabled="!dirty || config.environment_override">{{ busy ? '保存中…' : '保存验证码设置' }}</AdminButton></div>
      </form>
    </template>
  </div>
</template>
