<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { AdminAlert, AdminBadge, AdminButton, AdminEmpty, AdminInlineForm, AdminPageHeader, AdminRow } from "../components/ui";
import { operationsAdminApi as api, type Relay } from "../../infrastructure/operationsAdminApi";
const items = ref<Relay[]>([]);
const loading = ref(true);
const busy = ref(false);
const error = ref("");
const notice = ref("");
const inbox = ref("");
let alive = true;
const states: Record<string, string> = { idle: "未启用", pending: "等待对方接受", accepted: "已连接", rejected: "已拒绝" };
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
    <AdminPageHeader title="联邦中继" description="通过中继与其他服务器交换公开帖子。" />
    <AdminAlert v-if="error" tone="danger">
      {{ error }}
      <template #action><AdminButton size="sm" :disabled="busy || loading" @click="load">重试加载</AdminButton></template>
    </AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <section class="admin-card" aria-label="添加中继">
      <AdminInlineForm v-model="inbox" label="中继收件箱地址" type="url" placeholder="https://relay.example/inbox" button-label="添加中继" :busy="busy" :disabled="loading" @submit="add" />
    </section>
    <section class="admin-card" aria-label="中继列表" :aria-busy="loading">
      <div class="admin-card__header">
        <h2>已配置的中继</h2>
        <AdminButton size="sm" :disabled="busy || loading" @click="load">刷新</AdminButton>
      </div>
      <p v-if="loading" class="admin-empty" role="status">正在加载中继…</p>
      <AdminEmpty v-else-if="!items.length && !error">尚未添加中继。</AdminEmpty>
      <ul v-else class="admin-list">
        <AdminRow v-for="relay in items" :key="relay.id" :title="relay.inbox_url" mono>
          <template #meta><AdminBadge :tone="tones[relay.state] || 'neutral'">{{ states[relay.state] || relay.state }}</AdminBadge></template>
          <AdminButton size="sm" :disabled="busy" @click="act(relay, active(relay) ? 'disable' : 'enable')">{{ active(relay) ? '停用' : '启用' }}</AdminButton>
          <AdminButton size="sm" variant="danger" :disabled="busy" @click="act(relay, 'delete')">删除</AdminButton>
        </AdminRow>
      </ul>
    </section>
  </div>
</template>
