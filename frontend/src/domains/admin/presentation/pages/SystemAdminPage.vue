<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { operationsAdminApi as api, type DetailedHealth } from "../../infrastructure/operationsAdminApi";
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
let alive = true;
onMounted(() => { void loadHealth(); void loadNative(); });
onUnmounted(() => {
    alive = false;
});
</script>
<template>
  <div class="admin-page">
    <header>
      <h1>Matrix 服务</h1>
      <p>查看内置 Matrix 客户端接口与系统依赖状态。</p>
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
  </div>
</template>

