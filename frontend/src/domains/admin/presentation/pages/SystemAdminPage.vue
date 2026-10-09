<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { operationsAdminApi as api, type MatrixConfig, type MatrixStatus, type DetailedHealth } from "../../infrastructure/operationsAdminApi";
const health = ref<DetailedHealth>();
const versions = ref<string[]>([]);
const healthLoading = ref(true);
const nativeLoading = ref(true);
const healthError = ref("");
const nativeError = ref("");
const serviceNames: Record<string, string> = { database: "数据库", storage: "媒体存储" };
const healthNames: Record<string, string> = { healthy: "正常", degraded: "降级", unhealthy: "异常" };
async function loadHealth() {
    healthLoading.value = true;
    healthError.value = "";
    try { const data = await api.health(); if (alive) health.value = data; }
    catch { if (alive) healthError.value = "无法获取系统健康检查，请重试。"; }
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
    catch { if (alive) nativeError.value = "内置 Matrix 客户端接口暂不可访问，请重试。"; }
    finally { if (alive) nativeLoading.value = false; }
}
const config = ref<MatrixConfig>();
const status = ref<MatrixStatus>();
const loading = ref(true);
const error = ref("");
const busy = ref(false);
const user = ref("");
const result = ref("");
const testError = ref("");
let alive = true;
async function load() {
    loading.value = true;
    error.value = "";
    try {
        const [s, c] = await Promise.all([api.matrixStatus(), api.matrixConfig()]);
        if (alive) {
            status.value = s;
            config.value = c;
        }
    }
    catch {
        if (alive)
            error.value = "无法读取 Matrix 桥接状态，请重试。";
    }
    finally {
        if (alive)
            loading.value = false;
    }
}
async function test() {
    if (busy.value)
        return;
    busy.value = true;
    result.value = "";
    testError.value = "";
    try {
        const r = await api.matrixTest(user.value.trim());
        if (!alive)
            return;
        if (r.success)
            result.value = `连接测试成功${r.display_name ? `：${r.display_name}` : ""}`;
        else
            testError.value = "未能读取该 Matrix 用户，请检查账号地址和服务连接。";
    }
    catch {
        if (alive)
            testError.value = "连接测试失败，请检查桥接配置后重试。";
    }
    finally {
        if (alive)
            busy.value = false;
    }
}
onMounted(() => { void load(); void loadHealth(); void loadNative(); });
onUnmounted(() => {
    alive = false;
});
</script>
<template>
  <div class="admin-page">
    <header>
      <h1>Matrix 服务</h1>
      <p>查看内置 Matrix 接口、系统依赖与桥接状态。</p>
    </header>
    <section class="admin-card" aria-label="内置 Matrix 服务">
      <div class="admin-card__header"><h2>内置 Matrix 服务</h2><button class="admin-button" :disabled="nativeLoading" @click="loadNative">检查客户端接口</button></div>
      <p v-if="nativeLoading" role="status">正在检查 Matrix 客户端接口…</p>
      <p v-else-if="nativeError" role="alert" class="admin-error">{{ nativeError }}</p>
      <template v-else><p role="status">客户端接口可访问</p><p>支持的协议版本：{{ versions.join('、') }}</p></template>
      <p>此检查读取本站 Matrix 协议版本，不能代替消息投递和联邦连通性测试。</p>
    </section>
    <section class="admin-card" aria-label="系统依赖">
      <div class="admin-card__header"><h2>系统依赖</h2><button class="admin-button" :disabled="healthLoading" @click="loadHealth">刷新健康检查</button></div>
      <p v-if="healthLoading" role="status">正在检查数据库与存储…</p>
      <p v-else-if="healthError" role="alert" class="admin-error">{{ healthError }}</p>
      <template v-else-if="health">
        <p>总体状态：{{ healthNames[health.status] || '未知' }} · 运行时长：{{ health.uptime }}</p>
        <dl><template v-for="(label, key) in serviceNames" :key="key"><dt>{{ label }}</dt><dd>{{ healthNames[health.services?.[key]?.status || ''] || '未提供检查结果' }}<span v-if="health.services?.[key]?.latency"> · {{ health.services[key]!.latency }}</span></dd></template></dl>
        <p>检查时间：{{ health.timestamp }}</p>
      </template>
    </section>
    <div class="admin-actions">
      <button class="admin-button" :disabled="loading || busy" @click="load">刷新状态</button>
    </div>
    <p v-if="loading" class="admin-empty" role="status">正在检查服务连接…</p>
    <p v-if="error" class="admin-error" role="alert">
      {{ error }} <button class="admin-button" :disabled="loading" @click="load">重试</button>
    </p>
    <template v-if="config && status && !error">
      <section class="admin-card">
        <h2>Matrix 桥接</h2>
        <p role="status">{{ !status.enabled ? '未配置或未启用' : status.connected ? '已连接 Homeserver' : '暂时无法连接 Homeserver' }}</p>
        <dl>
          <dt>Homeserver</dt>
          <dd>{{ config.homeserver_url || '未配置' }}</dd>
          <dt>服务器名称</dt>
          <dd>{{ config.server_name || '未配置' }}</dd>
          <dt>桥接账号</dt>
          <dd>{{ config.bridge_username || '未配置' }}</dd>
          <dt>用户命名空间</dt>
          <dd>{{ config.user_namespace || '未配置' }}</dd>
          <dt>房间命名空间</dt>
          <dd>{{ config.room_namespace || '未配置' }}</dd>
        </dl>
        <p>配置由服务器部署环境管理；此页面仅展示非敏感信息。</p>
      </section>
      <form class="admin-card" @submit.prevent="test">
        <h2>测试用户连接</h2>
        <p>通过已配置的桥接服务读取指定 Matrix 用户资料。</p>
        <label class="admin-field">Matrix 用户地址<input v-model="user" required pattern="@[^:]+:.+" placeholder="@user:example.org" :disabled="busy || !status.enabled" />
        </label>
        <div class="admin-form-actions"><button class="admin-button admin-button--primary" :disabled="busy || loading || !status.enabled">{{ busy ? '正在测试…' : '测试连接' }}</button></div>
        <p v-if="result" role="status">{{ result }}</p>
        <p v-if="testError" class="admin-error" role="alert">{{ testError }}</p>
      </form>
    </template>
  </div>
</template>

