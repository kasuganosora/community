<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { communityAssetsApi,type Advertisement } from '@/domains/admin/infrastructure/communityAssetsApi'
const items=ref<Advertisement[]>([])
let stopped=false,timer:ReturnType<typeof setInterval>|undefined
function safeLink(raw:string){try{const u=new URL(raw);return ['http:','https:'].includes(u.protocol)&&!u.username&&!u.password?u.href:''}catch{return ''}}
function localImage(raw:string){try{const u=new URL(raw,window.location.origin);return /^\/emoji-assets\/[a-zA-Z0-9._-]+$/.test(u.pathname)?u.pathname:''}catch{return ''}}
async function load(){try{const rows=await communityAssetsApi.advertisements();if(!stopped)items.value=rows.filter(a=>safeLink(a.target_url)&&localImage(a.image_url))}catch{if(!stopped)items.value=[]}}
// Refresh once a minute while visible; hidden pages make no requests and
// catch up when shown again if the last load is stale.
let loadedAt=0
async function refresh(){if(document.hidden)return;loadedAt=Date.now();await load()}
function onVisibility(){if(!document.hidden&&Date.now()-loadedAt>=60000)void refresh()}
onMounted(()=>{void refresh();timer=setInterval(()=>void refresh(),60000);document.addEventListener('visibilitychange',onVisibility)})
onUnmounted(()=>{stopped=true;if(timer)clearInterval(timer);document.removeEventListener('visibilitychange',onVisibility)})
</script>
<template><aside v-if="items.length" class="advertisements" aria-label="广告"><article v-for="item in items" :key="item.id"><span class="ad-label">广告</span><a :href="safeLink(item.target_url)" target="_blank" rel="sponsored noopener noreferrer" referrerpolicy="no-referrer"><img :src="localImage(item.image_url)" :alt="item.title" loading="lazy" /><span>{{ item.title }}</span></a></article></aside></template>
<style scoped>.advertisements{display:grid;gap:16px}.advertisements article{padding:12px;border:1px solid var(--color-border);border-radius:16px;background:var(--color-bg-card)}.ad-label{display:block;color:var(--color-text-secondary);font-size:12px;margin-bottom:8px}.advertisements a{display:grid;gap:8px;color:var(--color-text-primary);text-decoration:none}.advertisements img{width:100%;max-height:200px;object-fit:contain;border-radius:8px}</style>
