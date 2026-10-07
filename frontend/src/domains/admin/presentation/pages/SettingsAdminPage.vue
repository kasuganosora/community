<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { operationsAdminApi as api, type CaptchaConfig, type SiteRule } from "../../infrastructure/operationsAdminApi";
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
        const [r, c] = await Promise.all([api.rules(), api.captcha()]);
        if (alive) {
            rules.value = r;
            config.value = c;
            saved.value = JSON.stringify(c);
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
    <header>
      <h1>站点设置</h1>
      <p>维护公开的社区规则及注册验证码。</p>
    </header>
    <p v-if="loading" role="status">正在加载设置…</p>
    <p v-if="error" class="admin-error" role="alert">
      {{ error }} <button v-if="!config" class="admin-button" :disabled="loading" @click="load">重试加载</button>
    </p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <template v-if="config && !loading">
      <section class="admin-card">
        <h2>社区规则</h2>
        <p>规则会展示在站点介绍及注册页面。</p>
        <p v-if="!rules.length" class="admin-empty">尚未设置规则。</p>
        <ul class="rules">
          <li v-for="rule in rules" :key="rule.id">
            <p>{{ rule.text }}</p>
            <div class="admin-toolbar">
              <button class="admin-button" :disabled="busy" @click="editing = rule.id; text = rule.text">编辑规则</button>
              <button class="admin-button admin-button--danger" :disabled="busy" @click="remove(rule)">删除规则</button>
            </div>
          </li>
        </ul>
        <form @submit.prevent="saveRule">
          <label class="admin-field">{{ editing ? '编辑规则内容' : '新规则内容' }}<textarea v-model="text" required maxlength="255" :disabled="busy" />
          </label>
          <div class="admin-toolbar">
            <button class="admin-button admin-button--primary" :disabled="busy || !text.trim()">{{ busy ? '处理中…' : editing ? '保存规则' : '添加规则' }}</button>
            <button v-if="editing" class="admin-button" type="button" :disabled="busy" @click="editing = null; text = ''">取消编辑</button>
          </div>
        </form>
      </section>
      <form class="admin-card" @submit.prevent="saveCaptcha">
        <h2>验证码</h2>
        <p v-if="config.environment_override" role="status">验证码使用服务器环境配置，请由部署管理员修改；此处仅供查看。</p>
        <p v-else-if="config.enabled && config.effective_enabled === false" role="status">已设置启用，但服务尚未生效，请检查供应商和密钥配置。</p>
        <label class="admin-field">
          <span>
            <input v-model="config.enabled" type="checkbox" :disabled="busy || config.environment_override" /> 启用注册验证码</span>
        </label>
        <label class="admin-field">验证码服务<select v-model="config.provider" :disabled="busy || config.environment_override">
            <option value="none">无</option>
            <option value="local">本站图片验证码</option>
            <option value="recaptcha">Google reCAPTCHA</option>
            <option value="cloudflare">Cloudflare Turnstile</option>
            <option value="hcaptcha">hCaptcha</option>
          </select>
        </label>
        <label class="admin-field">站点公钥<input v-model="config.site_key" :disabled="busy || config.environment_override" autocomplete="off" />
        </label>
        <label class="admin-field">替换服务密钥<input v-model="secret" type="password" autocomplete="new-password" :disabled="busy || config.environment_override" placeholder="留空保留现有密钥" />
        </label>
        <p>现有服务密钥不会回显。仅在需要替换时填写；其他未显示配置保持不变。</p>
        <button class="admin-button admin-button--primary" :disabled="busy || !dirty || config.environment_override">{{ busy ? '保存中…' : '保存验证码设置' }}</button>
      </form>
    </template>
  </div>
</template>
<style scoped>.rules { list-style: none; padding: 0; } .rules li { padding-bottom: 16px; margin-bottom: 16px; border-bottom: 1px solid var(--color-separator); } .rules p { white-space: pre-wrap; overflow-wrap: anywhere; }</style>
