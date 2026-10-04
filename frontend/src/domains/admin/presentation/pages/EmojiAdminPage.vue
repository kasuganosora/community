<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { emojiAdminApi, type AdminEmoji, type EmojiFields } from '../../infrastructure/emojiAdminApi'

const origin = ref<'local' | 'remote'>('local')
const q = ref(''), category = ref(''), domain = ref('')
const page = ref(1), total = ref(0), limit = 40
const items = ref<AdminEmoji[]>([]), selected = ref<string[]>([])
const loading = ref(false), busy = ref(false), error = ref(''), notice = ref('')
const editor = ref(false), editing = ref<AdminEmoji | null>(null)
const copySource = ref<AdminEmoji | null>(null), copyName = ref('')
const imageId = ref(''), preview = ref(''), aliases = ref(''), uploadMeta = ref(''), previewNote = ref('')
const batchAction = ref('disable'), batchText = ref(''), batchValue = ref(true)
const roles = ref<{ id: string; name: string }[]>([]), rolesError = ref('')
const form = reactive<EmojiFields>(defaults())
let objectUrl = '', request = 0, uploadVersion = 0
function defaults(): EmojiFields {
  return { reaction_role_ids: [], shortcode: '', aliases: [], category: '', license: '', local_only: false, is_sensitive: false, visible_in_picker: true, disabled: false }
}
const pages = computed(() => Math.max(1, Math.ceil(total.value / limit)))
const allSelected = computed(() => items.value.length > 0 && items.value.every(e => selected.value.includes(e.id)))
const actionLabel: Record<string, string> = {
  enable: '启用', disable: '禁用', delete: '删除', set_category: '设置分类', set_license: '设置许可证',
  set_aliases: '替换别名', add_aliases: '添加别名', remove_aliases: '移除别名', set_local_only: '设置仅本地', set_sensitive: '设置敏感内容',
}
function message(e: unknown) { return e instanceof Error ? e.message : '操作失败，请重试' }
function formatSize(size: number) {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / (1024 * 1024)).toFixed(2)} MB`
}
function revokePreview() { if (objectUrl) URL.revokeObjectURL(objectUrl); objectUrl = '' }
async function load() {
  const version = ++request
  loading.value = true; error.value = ''; selected.value = []
  try {
    const result = await emojiAdminApi.list({ origin: origin.value, q: q.value, category: category.value, domain: domain.value, page: page.value, limit })
    if (version !== request) return
    items.value = result.items; total.value = result.total
  } catch (e) { if (version === request) { error.value = message(e); items.value = [] } }
  finally { if (version === request) loading.value = false }
}
function search() { page.value = 1; void load() }
function changeOrigin() { batchAction.value = 'disable'; search() }
function toggleAll() { selected.value = allSelected.value ? [] : items.value.map(e => e.id) }
function openEditor(emoji: AdminEmoji | null = null) {
  revokePreview(); uploadVersion++; editing.value = emoji; imageId.value = ''; uploadMeta.value = ''; previewNote.value = ''; error.value = ''; notice.value = ''
  const initial = defaults()
  for (const key of Object.keys(initial) as (keyof EmojiFields)[]) {
    Object.assign(form, { [key]: emoji?.[key] ?? initial[key] })
  }
  aliases.value = (emoji?.aliases || []).join(', '); preview.value = emoji?.url || ''; editor.value = true
}
function closeEditor() { if (busy.value) return; uploadVersion++; editor.value = false; revokePreview() }
async function upload(event: Event) {
  const input = event.target as HTMLInputElement, file = input.files?.[0]
  if (!file) return
  input.value = ''
  const allowed = ['image/png', 'image/apng', 'image/jpeg', 'image/gif', 'image/webp']
  if (!allowed.includes(file.type)) {
    error.value = `无法上传「${file.name || '未命名文件'}」：类型是 ${file.type || '未知'}。请改用 PNG、JPEG、GIF 或 WebP。`
    return
  }
  if (file.size > 2 * 1024 * 1024) {
    error.value = `无法上传「${file.name}」：大小为 ${formatSize(file.size)}，超过 2 MiB。`
    return
  }
  const version = ++uploadVersion
  busy.value = true; error.value = ''; previewNote.value = '正在上传并生成预览…'
  try {
    const result = await emojiAdminApi.upload(file)
    if (version !== uploadVersion) return
    imageId.value = result.id; revokePreview(); objectUrl = URL.createObjectURL(file); preview.value = objectUrl
    uploadMeta.value = `${file.name} · ${formatSize(file.size)} · ${file.type}`
    previewNote.value = '预览已生成。确认图片显示正常后，再点击保存。'
  } catch (e) { error.value = `上传失败：${message(e)}。预览没有更新，请换一张图片后再试。`; previewNote.value = '' }
  finally { busy.value = false }
}
function onPreviewLoad(event: Event) {
  const img = event.target as HTMLImageElement
  if (img.naturalWidth > 1024 || img.naturalHeight > 1024) {
    previewNote.value = `预览尺寸是 ${img.naturalWidth} × ${img.naturalHeight}，超过 1024 × 1024，保存时服务器可能会拒绝。`
  }
}
function onPreviewError() {
  previewNote.value = '预览失败：浏览器打不开这个文件。它可能已损坏，或不是支持的图片。'
}
function splitAliases(value: string) { return [...new Set(value.split(/[,，\n]/).map(s => s.trim()).filter(Boolean))] }
async function save() {
  if (busy.value) return
  if (!/^[a-zA-Z0-9_]{1,36}$/.test(form.shortcode)) { error.value = '短代码须为 1–36 个字母、数字或下划线'; return }
  if (!editing.value && !imageId.value) { error.value = '请先上传表情图片'; return }
  busy.value = true; error.value = ''
  try {
    const fields = editing.value?.domain
      ? { disabled: form.disabled, visible_in_picker: form.visible_in_picker }
      : { ...form, aliases: splitAliases(aliases.value), ...(imageId.value ? { image_id: imageId.value } : {}) }
    await emojiAdminApi.save(editing.value?.id || null, fields)
    editor.value = false; revokePreview(); notice.value = '表情已保存'; await load()
  } catch (e) { error.value = message(e) }
  finally { busy.value = false }
}
async function remove(emoji: AdminEmoji) {
  if (!window.confirm(`删除表情 :${emoji.shortcode}:？此操作无法撤销。`)) return
  busy.value = true; error.value = ''
  try { await emojiAdminApi.remove(emoji.id); notice.value = '表情已删除'; await load() }
  catch (e) { error.value = message(e) } finally { busy.value = false }
}
async function copy() {
  if (!copySource.value || busy.value) return
  if (!/^[a-zA-Z0-9_]{1,36}$/.test(copyName.value)) { error.value = '请输入有效的本地短代码'; return }
  busy.value = true; error.value = ''
  try { await emojiAdminApi.copy(copySource.value.id, copyName.value); copySource.value = null; notice.value = '已复制到本地表情'; await load() }
  catch (e) { error.value = message(e) } finally { busy.value = false }
}
async function batch() {
  if (!selected.value.length || busy.value) return
  if (batchAction.value === 'delete' && !window.confirm(`删除选中的 ${selected.value.length} 个表情？此操作无法撤销。`)) return
  busy.value = true; error.value = ''
  const action = batchAction.value
  const label = actionLabel[action] || '更新'
  const chosen = selected.value.length
  const fields = action === 'set_category' ? { category: batchText.value }
    : action === 'set_license' ? { license: batchText.value }
    : action.includes('aliases') ? { aliases: splitAliases(batchText.value) }
    : { value: batchValue.value }
  try {
    const result = await emojiAdminApi.bulk(selected.value, action, fields)
    notice.value = result.updated < chosen
      ? `批量${label}只完成了 ${result.updated} / ${chosen} 个。其余可能已被删除，或你没有权限修改。`
      : `已${label} ${result.updated} 个表情`
    await load()
  }
  catch (e) { error.value = `批量${label}失败：${message(e)}。选中的表情没有全部更新，请刷新后核对。` } finally { busy.value = false }
}
async function loadRoles() {
  rolesError.value = ''
  try { roles.value = await emojiAdminApi.roles() }
  catch (e) { rolesError.value = message(e) }
}
onMounted(() => { void load(); void loadRoles() })
onUnmounted(() => { request++; uploadVersion++; revokePreview() })
</script>

<template>
  <div class="emoji-admin">
    <div class="page-header"><h1 class="page-header__title">自定义表情管理</h1></div>
    <div class="admin-body">
      <p class="muted">管理本地表情，或将远端表情复制到本地。敏感表情会标记显示；仅本地表情不会向其他服务器提供。</p>
      <form class="toolbar" @submit.prevent="search">
        <label>来源<select v-model="origin" :disabled="busy" @change="changeOrigin"><option value="local">本地</option><option value="remote">远端</option></select></label>
        <label>搜索<input v-model="q" placeholder="短代码或别名" /></label>
        <label>分类<input v-model="category" placeholder="全部分类" /></label>
        <label v-if="origin === 'remote'">服务器<input v-model="domain" placeholder="example.org" /></label>
        <button class="btn btn--secondary btn--sm" :disabled="busy || loading">搜索</button>
        <button type="button" class="btn btn--primary btn--sm" :disabled="busy" @click="openEditor()">新建表情</button>
      </form>
      <p v-if="error" role="alert" class="error">{{ error }}</p>
      <p v-if="notice" role="status">{{ notice }}</p>

      <form v-if="editor" class="editor" aria-label="表情编辑" @submit.prevent="save">
        <h2>{{ editing ? '编辑表情' : '新建表情' }}</h2>
        <img v-if="preview" :src="preview" alt="表情预览" class="preview" @load="onPreviewLoad" @error="onPreviewError" />
        <p v-if="uploadMeta" class="muted">{{ uploadMeta }}</p>
        <p v-if="previewNote" class="muted">{{ previewNote }}</p>
        <template v-if="!editing?.domain">
          <label>图片<input type="file" accept="image/png,image/apng,image/jpeg,image/gif,image/webp" :disabled="busy" @change="upload" /></label>
          <p class="muted">支持 PNG / APNG、GIF、JPEG、WebP（含动画），不支持 SVG。最多 2 MiB，最大 1024 × 1024 像素。上传完成后点击保存。</p>
          <label>短代码<input v-model="form.shortcode" required pattern="[a-zA-Z0-9_]+" maxlength="36" :disabled="busy" /></label>
          <p v-if="editing" class="muted">修改短代码不会改写历史帖子；旧帖子中的原短代码可能不再显示为表情。</p>
          <label>别名<input v-model="aliases" placeholder="用逗号分隔" :disabled="busy" /></label>
          <label>分类<input v-model="form.category" :disabled="busy" /></label>
          <label>许可证<input v-model="form.license" placeholder="授权说明或许可证链接" :disabled="busy" /></label>
          <fieldset class="role-options">
            <legend>允许使用此表情反应的角色</legend>
            <p class="muted">不选择角色时所有人都可使用。</p>
            <p v-if="rolesError" role="alert">{{ rolesError }} <button type="button" :disabled="busy" @click="loadRoles">重试加载角色</button></p>
            <label v-for="role in roles" :key="role.id" class="check"><input v-model="form.reaction_role_ids" type="checkbox" :value="role.id" :disabled="busy" />{{ role.name }}</label>
            <p v-if="form.reaction_role_ids.some(id => !roles.some(role => role.id === id))" class="muted">部分已选角色未显示，将保留已有角色限制。</p>
          </fieldset>
          <label class="check"><input v-model="form.local_only" type="checkbox" :disabled="busy" />仅本地使用</label>
          <label class="check"><input v-model="form.is_sensitive" type="checkbox" :disabled="busy" />敏感内容</label>
        </template>
        <p v-else class="muted">远端表情只能调整启用状态和选择器可见性；复制到本地后可编辑其他信息。</p>
        <label class="check"><input v-model="form.visible_in_picker" type="checkbox" :disabled="busy" />在表情选择器中显示</label>
        <label class="check"><input v-model="form.disabled" type="checkbox" :disabled="busy" />禁用表情</label>
        <div class="toolbar"><button class="btn btn--primary btn--sm" :disabled="busy">{{ busy ? '处理中…' : '保存' }}</button><button type="button" class="btn btn--secondary btn--sm" :disabled="busy" @click="closeEditor">取消</button></div>
      </form>
      <form v-if="copySource" class="editor" aria-label="复制远端表情" @submit.prevent="copy">
        <h2>复制 :{{ copySource.shortcode }}: 到本地</h2>
        <label>本地短代码<input v-model="copyName" required pattern="[a-zA-Z0-9_]+" :disabled="busy" /></label>
        <div class="toolbar"><button class="btn btn--primary btn--sm" :disabled="busy">确认复制</button><button type="button" class="btn btn--secondary btn--sm" :disabled="busy" @click="copySource = null">取消</button></div>
      </form>

      <div v-if="loading" class="page-loading" role="status">正在加载表情…</div>
      <template v-else>
        <form v-if="items.length" class="toolbar batch" @submit.prevent="batch">
          <label class="check"><input type="checkbox" :checked="allSelected" :disabled="busy" @change="toggleAll" />本页全选（{{ selected.length }}）</label>
          <p v-if="!selected.length" class="muted">先勾选要处理的表情。批量失败时会写明原因，不会悄悄跳过。</p>
          <label>批量操作<select v-model="batchAction" :disabled="busy"><option value="enable">启用</option><option value="disable">禁用</option><option value="delete">删除</option><template v-if="origin === 'local'"><option value="set_category">设置分类</option><option value="set_license">设置许可证</option><option value="set_aliases">替换别名</option><option value="add_aliases">添加别名</option><option value="remove_aliases">移除别名</option><option value="set_local_only">设置仅本地</option><option value="set_sensitive">设置敏感内容</option></template></select></label>
          <label v-if="['set_category', 'set_license', 'set_aliases', 'add_aliases', 'remove_aliases'].includes(batchAction)">内容<input v-model="batchText" placeholder="留空可清除；别名用逗号分隔" :disabled="busy" /></label>
          <label v-if="['set_local_only', 'set_sensitive'].includes(batchAction)" class="check"><input v-model="batchValue" type="checkbox" :disabled="busy" />开启</label>
          <button class="btn btn--secondary btn--sm" :disabled="busy || !selected.length">应用到选中项</button>
        </form>
        <ul class="emoji-list">
          <li v-for="emoji in items" :key="emoji.id">
            <input v-model="selected" type="checkbox" :value="emoji.id" :aria-label="`选择 ${emoji.shortcode}`" :disabled="busy" />
            <img :src="emoji.static_url || emoji.url" :alt="`:${emoji.shortcode}:`" loading="lazy" />
            <div class="emoji-info"><strong>:{{ emoji.shortcode }}:</strong><p class="muted">{{ emoji.domain || '本地' }} · {{ emoji.category || '未分类' }}</p><p v-if="emoji.aliases?.length" class="muted">别名：{{ emoji.aliases.join('、') }}</p><p class="muted">{{ [emoji.disabled ? '已禁用' : '已启用', emoji.local_only ? '仅本地' : '', emoji.is_sensitive ? '敏感内容' : '', !emoji.visible_in_picker ? '选择器隐藏' : ''].filter(Boolean).join(' · ') }}</p></div>
            <div class="actions"><button type="button" class="btn btn--secondary btn--sm" :disabled="busy" @click="openEditor(emoji)">编辑</button><button v-if="emoji.domain" type="button" class="btn btn--secondary btn--sm" :disabled="busy" @click="copySource = emoji; copyName = emoji.shortcode">复制到本地</button><button type="button" class="btn btn--secondary btn--sm" :disabled="busy" @click="remove(emoji)">删除</button></div>
          </li>
        </ul>
        <p v-if="!items.length && !error" class="empty-state">没有符合条件的表情</p>
        <nav class="toolbar" aria-label="表情分页"><button class="btn btn--secondary btn--sm" :disabled="busy || page <= 1" @click="page--; load()">上一页</button><span>第 {{ page }} / {{ pages }} 页 · 共 {{ total }} 个</span><button class="btn btn--secondary btn--sm" :disabled="busy || page >= pages" @click="page++; load()">下一页</button></nav>
      </template>
    </div>
  </div>
</template>

<style scoped>
.admin-body { padding: var(--space-4) var(--gutter); display: grid; gap: var(--space-4); }
.toolbar { display: flex; gap: var(--space-3); align-items: end; flex-wrap: wrap; }
label { display: grid; gap: var(--space-1); font-size: var(--text-footnote); }
input:not([type='checkbox']), select { min-height: 40px; max-width: 100%; border: 1px solid var(--color-border); border-radius: var(--radius-sm); padding: var(--space-2); background: var(--color-bg-grouped); color: var(--color-label); }
.check { display: flex; align-items: center; gap: var(--space-2); min-height: 40px; }
.editor { display: grid; gap: var(--space-3); padding: var(--space-4); border: 1px solid var(--color-border); border-radius: var(--radius-md); }
.preview { width: 80px; height: 80px; object-fit: contain; }
.muted { color: var(--color-label-secondary); font-size: var(--text-footnote); overflow-wrap: anywhere; }
.error { color: var(--color-danger, #b42318); }
.emoji-list { list-style: none; padding: 0; }
.emoji-list li { display: flex; align-items: center; flex-wrap: wrap; gap: var(--space-3); padding: var(--space-3) 0; border-bottom: 1px solid var(--color-border); }
.emoji-list img { width: 40px; height: 40px; object-fit: contain; }
.emoji-info { flex: 1; min-width: 120px; overflow-wrap: anywhere; }
.actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
button:disabled { opacity: .5; }
</style>
