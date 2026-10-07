<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { communityAssetsApi, type ManagedFile } from '../../infrastructure/communityAssetsApi'
const items=ref<ManagedFile[]>([]),total=ref(0),bytes=ref(0),page=ref(1),q=ref(''),owner=ref(''),kind=ref(''),attached=ref(''),error=ref(''),loading=ref(false),removing=ref('')
const size=(n:number)=>n<1024?`${n} B`:n<1048576?`${(n/1024).toFixed(1)} KiB`:`${(n/1048576).toFixed(2)} MiB`
async function load(){loading.value=true;error.value='';try{const result=await communityAssetsApi.files({page:page.value,q:q.value,account_id:owner.value,type:kind.value,attached:attached.value});items.value=result.items;total.value=result.total;bytes.value=result.bytes}catch{error.value='无法加载文件，请重试'}finally{loading.value=false}}
function search(){page.value=1;void load()}
async function remove(item:ManagedFile){if(!window.confirm(`永久删除「${item.name}」及本地文件？此操作不可撤销。仍被帖子、头像或其他附件引用的文件不会删除。`))return;removing.value=item.id;error.value='';try{await communityAssetsApi.deleteFile(item.id);await load()}catch{error.value='删除失败：文件可能已有新引用，请刷新后查看原因。'}finally{removing.value=''}}
onMounted(load)
</script>
<template>
 <section class="admin-page">
<header class="admin-page-header"><div><h1>文件管理</h1><p>查看真实附件与占用。已引用文件受到保护；文件预览仅限管理员查看。</p></div></header>
 <form class="admin-toolbar" @submit.prevent="search"><label>文件名<input v-model="q" class="admin-input" /></label><label>账户 ID<input v-model="owner" class="admin-input" inputmode="numeric" /></label><label>类型<select v-model="kind" class="admin-select"><option value="">全部</option><option value="image">图片</option><option value="video">视频</option><option value="audio">音频</option><option value="unknown">其他</option></select></label><label>引用<select v-model="attached" class="admin-select"><option value="">全部</option><option value="true">帖子引用</option><option value="false">未关联帖子</option></select></label><button class="admin-button" :disabled="loading">筛选</button></form>
 <p role="status">共 {{ total }} 个文件 · {{ size(bytes) }}</p><p v-if="error" role="alert">{{ error }}</p>
 <div class="admin-table-wrap"><table class="admin-table"><thead><tr><th>文件</th><th>所有者</th><th>大小 / 类型</th><th>使用状态</th><th>操作</th></tr></thead><tbody><tr v-for="item in items" :key="item.id"><td><a :href="item.url" target="_blank" rel="noopener noreferrer">{{ item.name }}</a><small v-if="item.remote">远端缓存</small></td><td>{{ item.account_id }}</td><td>{{ size(item.size) }}<br />{{ item.content_type }}</td><td>{{ item.delete_blocked || '无已知引用，可删除' }}</td><td><button class="admin-button" :disabled="!!item.delete_blocked || !!removing" @click="remove(item)">{{ removing===item.id?'删除中…':'永久删除' }}</button></td></tr><tr v-if="!loading&&!items.length"><td colspan="5">没有匹配的文件</td></tr></tbody></table></div>
 <nav class="admin-toolbar" aria-label="文件分页"><button class="admin-button" :disabled="page<=1||loading" @click="page--;load()">上一页</button><span>第 {{ page }} 页</span><button class="admin-button" :disabled="page*30>=total||loading" @click="page++;load()">下一页</button></nav>
</section>
</template>
