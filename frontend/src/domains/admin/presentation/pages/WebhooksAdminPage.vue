<script setup lang="ts">
import { AdminAlert, AdminBadge, AdminButton, AdminEmpty, AdminField, AdminPageHeader } from "../components/ui"
import {onMounted,ref} from 'vue'
import {federationJobsApi as api,type Webhook,type HookDelivery} from '../../infrastructure/federationJobsApi'
import { t } from '@/shared/i18n'
const hooks=ref<Webhook[]>([]),deliveries=ref<HookDelivery[]>([]),busy=ref(false),error=ref(''),secret=ref(''),notice=ref(''),url=ref(''),description=ref(''),events=ref(['status.created']),selected=ref(''),state=ref(''),page=ref(1),total=ref(0),hookPage=ref(1),hookTotal=ref(0)
const supported=['status.created','status.updated','status.deleted']
async function load(){busy.value=true;error.value='';try{const [h,d]=await Promise.all([api.hooks(hookPage.value),api.deliveries(page.value,selected.value,state.value)]);hooks.value=h.items||[];hookTotal.value=h.total;deliveries.value=d.items||[];total.value=d.total}catch{error.value=t('admin.WebhooksAdminPage.s36')}finally{busy.value=false}}
async function create(){if(busy.value)return;busy.value=true;error.value='';try{const r=await api.createHook({url:url.value,description:description.value,events:events.value,enabled:true});secret.value=r.secret;url.value='';description.value='';notice.value=t('admin.WebhooksAdminPage.s37');await load()}catch{error.value=t('admin.WebhooksAdminPage.s38')}finally{busy.value=false}}
async function action(h:Webhook,kind:'enable'|'rotate'|'test'){if(busy.value)return;const prompt=kind==='rotate'?t('admin.WebhooksAdminPage.s39'):kind==='test'?t('admin.WebhooksAdminPage.s40'):t('admin.WebhooksAdminPage.s41', [h.enabled?t('admin.WebhooksAdminPage.s15'):t('admin.WebhooksAdminPage.s16')]);if(!window.confirm(prompt))return;busy.value=true;error.value='';try{if(kind==='enable')await api.updateHook(h.id,{enabled:!h.enabled});else{const r=await api.hookAction(h.id,kind);if(r.secret)secret.value=r.secret}notice.value=kind==='test'?t('admin.WebhooksAdminPage.s42'):t('admin.WebhooksAdminPage.s43');await load()}catch{error.value=t('admin.WebhooksAdminPage.s44')}finally{busy.value=false}}
async function deliveryAction(d:HookDelivery,action:'retry'|'cancel'){if(busy.value||!window.confirm(action==='cancel'?t('admin.WebhooksAdminPage.s45'):t('admin.WebhooksAdminPage.s46')))return;busy.value=true;try{await api.deliveryAction(d.id,action);await load()}catch{error.value=t('admin.WebhooksAdminPage.s47')}finally{busy.value=false}}
onMounted(load)
</script>
<template>
  <div class="admin-page">
    <AdminPageHeader title="Webhook" :description="$t('admin.WebhooksAdminPage.s1')" />
    <AdminAlert v-if="error" tone="danger">{{ error }}</AdminAlert>
    <AdminAlert v-if="notice" tone="success">{{ notice }}</AdminAlert>
    <p v-if="busy" class="admin-empty" role="status">{{ $t('admin.WebhooksAdminPage.s2') }}</p>
    <section v-if="secret" class="admin-card">
      <h2>{{ $t('admin.WebhooksAdminPage.s3') }}</h2>
      <p>{{ $t('admin.WebhooksAdminPage.s4') }}</p>
      <code>{{ secret }}</code>
      <div class="admin-form-actions"><AdminButton @click="secret=''">{{ $t('admin.WebhooksAdminPage.s5') }}</AdminButton></div>
    </section>
    <form class="admin-card" @submit.prevent="create">
      <h2>{{ $t('admin.WebhooksAdminPage.s6') }}</h2>
      <div class="admin-form">
        <AdminField :label="$t('admin.WebhooksAdminPage.s7')"><template #default="{ id }"><input :id="id" v-model="url" type="url" required maxlength="2048" placeholder="https://receiver.example/webhook" /></template></AdminField>
        <AdminField :label="$t('admin.WebhooksAdminPage.s8')"><template #default="{ id }"><input :id="id" v-model="description" maxlength="200" /></template></AdminField>
        <fieldset class="admin-check-group"><legend>{{ $t('admin.WebhooksAdminPage.s9') }}</legend><label v-for="event in supported" :key="event" class="admin-check"><input v-model="events" type="checkbox" :value="event" /><span class="admin-check__text">{{ event }}</span></label></fieldset>
      </div>
      <div class="admin-form-actions"><AdminButton type="submit" variant="primary" :disabled="busy||!events.length">{{ $t('admin.WebhooksAdminPage.s10') }}</AdminButton></div>
    </form>
    <section class="admin-card">
      <h2>{{ $t('admin.WebhooksAdminPage.s11') }}</h2>
      <AdminEmpty v-if="!hooks.length&&!busy&&!error">{{ $t('admin.WebhooksAdminPage.s12') }}</AdminEmpty>
      <article v-for="h in hooks" :key="h.id" class="admin-card">
        <div class="admin-card__header"><strong>{{ h.description||h.url }}</strong><AdminBadge :tone="h.enabled ? 'success' : 'neutral'">{{ h.enabled?$t('admin.WebhooksAdminPage.s13'):$t('admin.WebhooksAdminPage.s14') }}</AdminBadge></div>
        <p>{{ h.url }}</p>
        <p>{{ h.events.join('、') }}</p>
        <div class="admin-actions">
          <AdminButton size="sm" :disabled="busy" @click="action(h,'enable')">{{ h.enabled?$t('admin.WebhooksAdminPage.s15'):$t('admin.WebhooksAdminPage.s16') }}</AdminButton>
          <AdminButton size="sm" :disabled="busy||!h.enabled" @click="action(h,'test')">{{ $t('admin.WebhooksAdminPage.s17') }}</AdminButton>
          <AdminButton size="sm" :disabled="busy" @click="action(h,'rotate')">{{ $t('admin.WebhooksAdminPage.s18') }}</AdminButton>
        </div>
      </article>
      <div class="admin-pagination">
        <AdminButton size="sm" :disabled="busy||hookPage===1" @click="hookPage--;load()">{{ $t('admin.WebhooksAdminPage.s19') }}</AdminButton>
        <span v-if="!busy && !error">{{ $t('admin.WebhooksAdminPage.s20', [hookPage, hookTotal]) }}</span>
        <AdminButton size="sm" :disabled="busy||hookPage*30>=hookTotal" @click="hookPage++;load()">{{ $t('admin.WebhooksAdminPage.s21') }}</AdminButton>
      </div>
    </section>
    <section class="admin-card">
      <h2>{{ $t('admin.WebhooksAdminPage.s22') }}</h2>
      <form class="admin-toolbar" @submit.prevent="page=1;load()">
        <AdminField :label="$t('admin.WebhooksAdminPage.s23')"><template #default="{ id }"><input :id="id" v-model="selected" /></template></AdminField>
        <AdminField :label="$t('admin.WebhooksAdminPage.s24')"><template #default="{ id }"><select :id="id" v-model="state"><option value="">{{ $t('admin.WebhooksAdminPage.s25') }}</option><option v-for="value in ['pending','processing','delivered','failed','cancelled']" :key="value">{{ value }}</option></select></template></AdminField>
        <AdminButton type="submit" :disabled="busy">{{ $t('admin.WebhooksAdminPage.s26') }}</AdminButton>
      </form>
      <AdminEmpty v-if="!deliveries.length&&!busy&&!error">{{ $t('admin.WebhooksAdminPage.s27') }}</AdminEmpty>
      <article v-for="d in deliveries" :key="d.id" class="admin-card">
        <div class="admin-card__header"><strong>{{ d.event }}</strong><AdminBadge :tone="d.state==='delivered' ? 'success' : d.state==='failed' ? 'danger' : d.state==='pending' ? 'warning' : 'neutral'">{{ d.state }}</AdminBadge></div>
        <p>{{ $t('admin.WebhooksAdminPage.s28', [d.id, d.attempts]) }}</p>
        <p v-if="d.last_error" class="admin-error">{{ d.last_error }}</p>
        <div v-if="['pending','failed'].includes(d.state)" class="admin-actions">
          <AdminButton size="sm" :disabled="busy" @click="deliveryAction(d,'retry')">{{ $t('admin.WebhooksAdminPage.s29') }}</AdminButton>
          <AdminButton size="sm" variant="danger" :disabled="busy" @click="deliveryAction(d,'cancel')">{{ $t('admin.WebhooksAdminPage.s30') }}</AdminButton>
        </div>
      </article>
      <div class="admin-pagination">
        <AdminButton size="sm" :disabled="busy||page===1" @click="page--;load()">{{ $t('admin.WebhooksAdminPage.s31') }}</AdminButton>
        <span v-if="!busy && !error">{{ $t('admin.WebhooksAdminPage.s32', [page, total]) }}</span>
        <AdminButton size="sm" :disabled="busy||page*30>=total" @click="page++;load()">{{ $t('admin.WebhooksAdminPage.s33') }}</AdminButton>
      </div>
    </section>
    <details class="admin-card">
      <summary>{{ $t('admin.WebhooksAdminPage.s34') }}</summary>
      <p>{{ $t('admin.WebhooksAdminPage.s35') }}</p>
    </details>
  </div>
</template>
