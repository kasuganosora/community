<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { operationsAdminApi as api, type Relay } from "../../infrastructure/operationsAdminApi";
const items = ref<Relay[]>([]);
const loading = ref(true);
const busy = ref(false);
const error = ref("");
const notice = ref("");
const inbox = ref("");
let alive = true;
const states: Record<string, string> = { idle: "未启用", pending: "等待对方接受", accepted: "已连接", rejected: "已拒绝" };
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
            error.value = "无法加载中继，请重试。";
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
        notice.value = "中继已添加，等待对方接受。";
        await load();
    }
    catch {
        error.value = "添加中继失败，请检查收件箱地址及服务器连接后刷新列表。";
    }
    finally {
        busy.value = false;
    }
}
async function act(relay: Relay, action: "enable" | "disable" | "delete") {
    const label = action === "delete" ? "删除" : action === "disable" ? "停用" : "启用";
    if (busy.value || !window.confirm(`确认${label}中继 ${relay.inbox_url}？${action === "delete" ? "删除后需要重新添加。" : "这会改变公开帖子的中继投递。"}`))
        return;
    busy.value = true;
    error.value = "";
    notice.value = "";
    try {
        await api.relayAction(relay.id, action);
        notice.value = `中继已${label}。`;
        await load();
    }
    catch {
        error.value = `${label}失败，请刷新列表确认当前状态后重试。`;
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
    <header>
      <h1>联邦中继</h1>
      <p>管理 ActivityPub 中继，用于与其他服务器交换公开帖子。</p>
    </header>
    <p v-if="error" class="admin-error" role="alert">
      {{ error }} <button class="admin-button" :disabled="busy || loading" @click="load">重试加载</button>
    </p>
    <p v-if="notice" role="status">{{ notice }}</p>
    <form class="admin-card admin-toolbar" @submit.prevent="add">
      <label class="admin-field">中继收件箱地址<input v-model="inbox" type="url" required placeholder="https://relay.example/inbox" :disabled="busy" />
      </label>
      <button class="admin-button admin-button--primary" :disabled="busy || loading">{{ busy ? '处理中…' : '添加中继' }}</button>
    </form>
    <section class="admin-card" aria-label="中继列表" :aria-busy="loading">
      <div class="admin-toolbar">
        <h2>已配置的中继</h2>
        <button class="admin-button" :disabled="busy || loading" @click="load">刷新</button>
      </div>
      <p v-if="loading" role="status">正在加载中继…</p>
      <p v-else-if="!items.length && !error" class="admin-empty">尚未添加中继。</p>
      <ul v-else class="relay-list">
        <li v-for="relay in items" :key="relay.id">
          <strong>{{ relay.inbox_url }}</strong>
          <span class="admin-badge">{{ states[relay.state] || relay.state }}</span>
          <div class="admin-toolbar">
            <button class="admin-button" :disabled="busy" @click="act(relay, ['pending', 'accepted'].includes(relay.state) ? 'disable' : 'enable')">{{ ['pending', 'accepted'].includes(relay.state) ? '停用' : '启用' }}</button>
            <button class="admin-button admin-button--danger" :disabled="busy" @click="act(relay, 'delete')">删除</button>
          </div>
        </li>
      </ul>
    </section>
  </div>
</template>
<style scoped>
.relay-list { list-style: none; padding: 0; } .relay-list li { display: grid; gap: 12px; padding: 16px 0; border-bottom: 1px solid var(--color-separator); } .relay-list strong { overflow-wrap: anywhere; }
</style>
