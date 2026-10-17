<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { communityAssetsApi,type Advertisement } from '@/domains/admin/infrastructure/communityAssetsApi'
const items=ref<Advertisement[]>([])
let stopped=false,timer:ReturnType<typeof setInterval>|undefined
function safeLink(raw:string){try{const u=new URL(raw);return ['http:','https:'].includes(u.protocol)&&!u.username&&!u.password?u.href:''}catch{return ''}}
function localImage(raw:string){try{const u=new URL(raw,window.location.origin);return /^\/emoji-assets\/[a-zA-Z0-9._-]+$/.test(u.pathname)?u.pathname:''}catch{return ''}}
// Ads are not first-screen content: wait for the browser to go idle after
// mount, reuse a cached answer (including an empty list) for 5 minutes, and
// refresh at most every 5 minutes while visible.
const TTL=5*60*1000,KEY='community:ads:v1'
type Cached={at:number,rows:Advertisement[]}
function show(rows:Advertisement[]){if(!stopped)items.value=rows.filter(a=>safeLink(a.target_url)&&localImage(a.image_url))}
function readCache():Cached|null{try{const c=JSON.parse(localStorage.getItem(KEY)||'null') as Cached|null;return c&&Array.isArray(c.rows)&&typeof c.at==='number'?c:null}catch{return null}}
let loadedAt=0
async function refresh(force=false){
  if(document.hidden||stopped)return
  const c=readCache()
  if(!force&&c&&Date.now()-c.at<TTL){loadedAt=c.at;show(c.rows);return}
  loadedAt=Date.now()
  try{const rows=await communityAssetsApi.advertisements();try{localStorage.setItem(KEY,JSON.stringify({at:loadedAt,rows}))}catch{ /* Storage may be unavailable; the fetched rows can still be displayed. */ }show(rows)}catch{if(c)show(c.rows)}
}
function onVisibility(){if(!document.hidden&&Date.now()-loadedAt>=TTL)void refresh()}
let idle:number|undefined
onMounted(()=>{
  const c=readCache();if(c)show(c.rows)
  const start=()=>{void refresh();timer=setInterval(()=>void refresh(),TTL)}
  const w=window as Window&{requestIdleCallback?:(cb:()=>void,o?:{timeout:number})=>number}
  if(w.requestIdleCallback)idle=w.requestIdleCallback(start,{timeout:4000});else idle=window.setTimeout(start,2000)
  document.addEventListener('visibilitychange',onVisibility)
})
onUnmounted(()=>{stopped=true;if(timer)clearInterval(timer);const w=window as Window&{cancelIdleCallback?:(id:number)=>void};if(idle!==undefined){w.cancelIdleCallback?.(idle);clearTimeout(idle)}document.removeEventListener('visibilitychange',onVisibility)})
</script>
<template><aside v-if="items.length" class="advertisements" :aria-label="$t('shared.AdvertisementCard.s1')"><article v-for="item in items" :key="item.id"><span class="ad-label">{{ $t('shared.AdvertisementCard.s1') }}</span><a :href="safeLink(item.target_url)" target="_blank" rel="sponsored noopener noreferrer" referrerpolicy="no-referrer"><img :src="localImage(item.image_url)" :alt="item.title" loading="lazy" /><span>{{ item.title }}</span></a></article></aside></template>
<style scoped>.advertisements{display:grid;gap:16px}.advertisements article{padding:12px;border:1px solid var(--color-border);border-radius:16px;background:var(--color-bg-card)}.ad-label{display:block;color:var(--color-text-secondary);font-size:12px;margin-bottom:8px}.advertisements a{display:grid;gap:8px;color:var(--color-text-primary);text-decoration:none}.advertisements img{width:100%;max-height:200px;object-fit:contain;border-radius:8px}</style>
