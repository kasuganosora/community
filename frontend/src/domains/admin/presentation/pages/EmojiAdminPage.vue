<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref } from 'vue'
import { emojiAdminApi, type AdminEmoji, type EmojiFields } from '../../infrastructure/emojiAdminApi'

const origin = ref<'local' | 'remote'>('local')
const q = ref(''), category = ref(''), domain = ref('')
const page = ref(1), total = ref(0), limit = 40
const items = ref<AdminEmoji[]>([]), selected = ref<string[]>([])
const loading = ref(false), busy = ref(false), error = ref(''), notice = ref('')
const editorElement = ref<HTMLFormElement>()
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
  void nextTick(() => { editorElement.value?.scrollIntoView?.({ block: 'start', behavior: 'instant' }); editorElement.value?.querySelector<HTMLInputElement>('input:not([type=file])')?.focus({ preventScroll: true }) })
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
  <div class="admin-page emoji-admin">
    <header class="emoji-heading">
      <div><p class="emoji-eyebrow">社区表达</p><h1>自定义表情管理</h1><p class="muted">整理本站表情，也可以把喜欢的远端表情复制到本站。</p></div>
      <button type="button" class="admin-button admin-button--primary" :disabled="busy" @click="openEditor()"><t-icon name="add" aria-hidden="true" />新建表情</button>
    </header>

    <form class="admin-card emoji-search" aria-label="筛选表情" @submit.prevent="search">
      <label>来源<select v-model="origin" :disabled="busy" @change="changeOrigin"><option value="local">本站表情</option><option value="remote">远端表情</option></select></label>
      <label class="emoji-search__query">搜索<input v-model="q" type="search" placeholder="短代码或别名" :disabled="busy" /></label>
      <label>分类<input v-model="category" placeholder="全部分类" :disabled="busy" /></label>
      <label v-if="origin === 'remote'">服务器<input v-model="domain" placeholder="example.org" :disabled="busy" /></label>
      <button class="admin-button" :disabled="busy || loading"><t-icon name="search" aria-hidden="true" />搜索</button>
    </form>
    <p v-if="error" role="alert" class="admin-error emoji-feedback">{{ error }}</p>
    <p v-if="notice" role="status" class="emoji-feedback emoji-feedback--success"><t-icon name="check-circle" aria-hidden="true" />{{ notice }}</p>

    <form v-if="editor" ref="editorElement" class="admin-card editor" aria-label="表情编辑" @submit.prevent="save">
      <div class="emoji-section-heading"><div><h2>{{ editing ? '编辑表情' : '新建表情' }}</h2><p class="muted">{{ editing?.domain ? '调整此表情在本站的显示方式。' : '上传图片，为它起一个容易记住的名字。' }}</p></div><button type="button" class="admin-button" :disabled="busy" aria-label="关闭表情编辑" @click="closeEditor"><t-icon name="close" aria-hidden="true" /></button></div>
      <div class="editor-grid">
        <div class="editor-artwork">
          <div class="preview-stage"><img v-if="preview" :src="preview" alt="表情预览" class="preview" @load="onPreviewLoad" @error="onPreviewError" /><div v-else class="preview-empty"><t-icon name="image-add" :size="36" aria-hidden="true" /><span>图片预览</span></div></div>
          <label v-if="!editing?.domain" class="upload-label"><span class="upload-button"><t-icon name="image-add" aria-hidden="true" />{{ preview ? '更换图片' : '选择图片' }}</span><input type="file" accept="image/png,image/apng,image/jpeg,image/gif,image/webp" :disabled="busy" @change="upload" /></label>
          <p v-if="uploadMeta" class="muted">{{ uploadMeta }}</p><p v-if="previewNote" class="muted" role="status">{{ previewNote }}</p>
          <p v-if="!editing?.domain" class="muted upload-hint">PNG / APNG、GIF、JPEG、WebP（含动画）<br />不支持 SVG · 最大 2 MiB · 1024 × 1024 像素</p>
        </div>
        <div class="editor-fields">
          <template v-if="!editing?.domain">
            <label>短代码<input v-model="form.shortcode" required pattern="[a-zA-Z0-9_]+" maxlength="36" :disabled="busy" placeholder="例如 happy_cat" /><small class="muted">在帖子中输入 :{{ form.shortcode || 'shortcode' }}: 即可使用。</small></label>
            <p v-if="editing" class="muted">改名不会改写历史帖子，原短代码可能不再显示为表情。</p>
            <label>别名<input v-model="aliases" placeholder="例如 开心, 猫咪" :disabled="busy" /><small class="muted">用逗号分隔，方便成员搜索。</small></label>
            <div class="editor-fields__pair"><label>分类<input v-model="form.category" placeholder="例如 日常表情" :disabled="busy" /></label><label>许可证<input v-model="form.license" placeholder="授权说明或许可证链接" :disabled="busy" /></label></div>
            <details class="role-options" :open="!!rolesError || form.reaction_role_ids.length > 0"><summary>允许使用此表情反应的角色 <span class="muted">{{ form.reaction_role_ids.length ? `已选 ${form.reaction_role_ids.length} 个` : '所有人' }}</span></summary><p class="muted">不选择角色时所有人都可使用。</p><p v-if="rolesError" role="alert">{{ rolesError }} <button type="button" class="admin-button" :disabled="busy" @click="loadRoles">重试加载角色</button></p><label v-for="role in roles" :key="role.id" class="check"><input v-model="form.reaction_role_ids" type="checkbox" :value="role.id" :disabled="busy" />{{ role.name }}</label><p v-if="form.reaction_role_ids.some(id => !roles.some(role => role.id === id))" class="muted">部分已选角色未显示，将保留已有角色限制。</p></details>
          </template>
          <p v-else class="muted">远端表情的名称和图片由原服务器维护。如需修改，请先复制到本站。</p>
          <div class="editor-options">
            <template v-if="!editing?.domain"><label class="check"><input v-model="form.local_only" type="checkbox" :disabled="busy" /><span>仅本地使用<small>不向其他服务器提供此表情</small></span></label><label class="check"><input v-model="form.is_sensitive" type="checkbox" :disabled="busy" /><span>敏感内容<small>成员需主动选择显示</small></span></label></template>
            <label class="check"><input v-model="form.visible_in_picker" type="checkbox" :disabled="busy" /><span>在表情选择器中显示</span></label>
            <label class="check"><input v-model="form.disabled" type="checkbox" :disabled="busy" /><span>禁用表情<small>暂停成员使用此表情</small></span></label>
          </div>
        </div>
      </div>
      <div class="editor-footer"><button type="button" class="admin-button" :disabled="busy" @click="closeEditor">取消</button><button class="admin-button admin-button--primary" :disabled="busy">{{ busy ? '处理中…' : '保存' }}</button></div>
    </form>

    <form v-if="copySource" class="admin-card copy-editor" aria-label="复制远端表情" @submit.prevent="copy"><h2>复制到本站</h2><p class="muted">为 :{{ copySource.shortcode }}: 设置本站短代码，复制后可以独立管理。</p><label>本地短代码<input v-model="copyName" required pattern="[a-zA-Z0-9_]+" maxlength="36" :disabled="busy" /></label><div class="editor-footer"><button type="button" class="admin-button" :disabled="busy" @click="copySource = null">取消</button><button class="admin-button admin-button--primary" :disabled="busy">确认复制</button></div></form>

    <div v-if="loading" class="admin-card emoji-empty" role="status">正在加载表情…</div>
    <template v-else>
      <div v-if="items.length" class="emoji-results-heading"><label class="check"><input type="checkbox" :checked="allSelected" :disabled="busy" @change="toggleAll" />本页全选（{{ selected.length }}）</label><span class="muted">{{ total }} 个表情</span></div>
      <form v-if="selected.length" class="admin-card batch" aria-label="批量管理表情" @submit.prevent="batch">
        <div class="batch-heading"><strong>已选择 {{ selected.length }} 个</strong><button type="button" class="admin-button" :disabled="busy" @click="selected = []">取消选择</button></div>
        <div class="batch-fields"><label>批量操作<select v-model="batchAction" :disabled="busy"><option value="enable">启用</option><option value="disable">禁用</option><option value="delete">删除</option><template v-if="origin === 'local'"><option value="set_category">设置分类</option><option value="set_license">设置许可证</option><option value="set_aliases">替换别名</option><option value="add_aliases">添加别名</option><option value="remove_aliases">移除别名</option><option value="set_local_only">设置仅本地</option><option value="set_sensitive">设置敏感内容</option></template></select></label><label v-if="['set_category', 'set_license', 'set_aliases', 'add_aliases', 'remove_aliases'].includes(batchAction)">内容<input v-model="batchText" placeholder="留空可清除；别名用逗号分隔" :disabled="busy" /></label><label v-if="['set_local_only', 'set_sensitive'].includes(batchAction)" class="check"><input v-model="batchValue" type="checkbox" :disabled="busy" />开启</label><button class="admin-button" :class="batchAction === 'delete' ? 'admin-button--danger' : 'admin-button--primary'" :disabled="busy || !selected.length">应用到选中项</button></div>
      </form>
      <ul class="emoji-list">
        <li v-for="emoji in items" :key="emoji.id" class="emoji-card" :class="{ 'emoji-card--selected': selected.includes(emoji.id), 'emoji-card--disabled': emoji.disabled }">
          <div class="emoji-card__top"><input v-model="selected" type="checkbox" :value="emoji.id" :aria-label="`选择 ${emoji.shortcode}`" :disabled="busy" /><span class="emoji-badge" :class="{ 'emoji-badge--disabled': emoji.disabled }">{{ emoji.disabled ? '已禁用' : '已启用' }}</span></div>
          <button type="button" class="emoji-card__artwork" :aria-label="`编辑 :${emoji.shortcode}:`" :disabled="busy" @click="openEditor(emoji)"><img :src="emoji.static_url || emoji.url" :alt="`:${emoji.shortcode}:`" loading="lazy" /></button>
          <div class="emoji-info"><strong>:{{ emoji.shortcode }}:</strong><p class="muted">{{ emoji.category || '未分类' }} · {{ emoji.domain || '本站' }}</p><p v-if="emoji.aliases?.length" class="muted">别名：{{ emoji.aliases.join('、') }}</p><div class="emoji-flags"><span v-if="emoji.local_only">仅本地</span><span v-if="emoji.is_sensitive">敏感内容</span><span v-if="!emoji.visible_in_picker">选择器隐藏</span></div></div>
          <div class="actions"><button type="button" class="admin-button" :disabled="busy" @click="openEditor(emoji)">编辑</button><button v-if="emoji.domain" type="button" class="admin-button" :disabled="busy" @click="copySource = emoji; copyName = emoji.shortcode">复制到本地</button><button type="button" class="admin-button admin-button--danger" :disabled="busy" @click="remove(emoji)">删除</button></div>
        </li>
      </ul>
      <div v-if="!items.length && !error" class="admin-card emoji-empty"><t-icon name="happy" :size="36" aria-hidden="true" /><h2>没有符合条件的表情</h2><p class="muted">试试其他关键词，或上传社区的第一个表情。</p></div>
      <nav class="emoji-pagination" aria-label="表情分页"><button class="admin-button" :disabled="busy || page <= 1" @click="page--; load()">上一页</button><span class="muted">第 {{ page }} / {{ pages }} 页 · 共 {{ total }} 个</span><button class="admin-button" :disabled="busy || page >= pages" @click="page++; load()">下一页</button></nav>
    </template>
  </div>
</template>

<style scoped>
.emoji-admin.admin-page { min-width: 0; background: transparent; border: 0; border-radius: 0; overflow: visible; }
.emoji-heading, .emoji-section-heading, .emoji-results-heading, .batch-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.emoji-heading > div, .emoji-section-heading > div { min-width: 0; }
.emoji-heading > button { flex-shrink: 0; }
.emoji-eyebrow { color: var(--admin-accent); font-size: 11px; font-weight: 600; letter-spacing: 2px; margin: 0 0 8px; }
.emoji-heading h1 { margin-bottom: 8px; }
.muted { color: var(--color-label-secondary); font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; }
.emoji-admin label { display: grid; gap: 7px; min-width: 0; font-size: 13px; font-weight: 500; }
.emoji-admin input:not([type=checkbox]), .emoji-admin select { width: 100%; min-width: 0; max-width: 100%; min-height: 42px; padding: 9px 12px; color: var(--color-label); background: var(--color-bg-row); border: 1px solid var(--color-separator); border-radius: 9px; box-sizing: border-box; }
.emoji-admin input[type=checkbox] { width: 17px; height: 17px; flex-shrink: 0; accent-color: var(--admin-accent); }
.emoji-search { display: flex; flex-wrap: wrap; gap: 14px; align-items: end; }
.emoji-search label { flex: 1 1 130px; }
.emoji-search .emoji-search__query { flex: 2 1 210px; }
.emoji-search > button { min-height: 42px; }
.emoji-feedback { margin: 0; padding: 14px 18px; border-radius: 12px; overflow-wrap: anywhere; }
.emoji-feedback--success { display: flex; gap: 8px; background: color-mix(in srgb, var(--admin-accent) 7%, var(--color-bg-row)); }
.editor { display: grid; gap: 24px; scroll-margin-top: 20px; }
.editor h2, .copy-editor h2 { margin: 0; }
.editor-grid { display: grid; grid-template-columns: minmax(0, 220px) minmax(0, 1fr); gap: 28px; }
.editor-artwork, .editor-fields { min-width: 0; }
.editor-fields { display: grid; gap: 18px; }
.editor-fields__pair { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
.preview-stage, .emoji-card__artwork { background-color: var(--color-bg-grouped); background-image: repeating-conic-gradient(transparent 0% 25%, color-mix(in srgb, var(--color-label) 3%, transparent) 0% 50%); background-size: 18px 18px; }
.preview-stage { min-height: 180px; border-radius: 14px; display: grid; place-items: center; padding: 20px; margin-bottom: 14px; }
.preview { width: 110px; height: 110px; object-fit: contain; }
.preview-empty { display: grid; justify-items: center; gap: 12px; font-size: 12px; color: var(--color-label-secondary); }
.upload-label { position: relative; cursor: pointer; }
.upload-label input[type=file] { position: absolute; width: 1px; height: 1px; min-height: 0; padding: 0; opacity: 0; clip-path: inset(50%); }
.upload-button { display: flex; align-items: center; justify-content: center; gap: 8px; min-height: 42px; border: 1px solid var(--color-separator); border-radius: 9px; background: var(--color-bg-row); font-size: 13px; }
.upload-label:focus-within .upload-button { outline: 3px solid var(--admin-accent); outline-offset: 2px; }
.upload-label input::file-selector-button { border: 0; border-radius: 5px; padding: 6px 8px; background: var(--color-fill); color: var(--color-label); margin-right: 8px; cursor: pointer; }
.upload-hint { font-size: 11px; }
.role-options { min-width: 0; border: 1px solid var(--color-separator); border-radius: 10px; padding: 14px; }
.role-options summary { cursor: pointer; font-size: 13px; overflow-wrap: anywhere; }
.role-options summary span { margin-left: 8px; }
.editor-options { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; padding-top: 6px; }
.emoji-admin .check { display: flex; align-items: center; gap: 9px; min-height: 32px; font-weight: 400; }
.check small { display: block; font-size: 11px; color: var(--color-label-secondary); margin-top: 3px; }
.editor-footer { display: flex; justify-content: flex-end; gap: 10px; border-top: 1px solid var(--color-separator); padding-top: 18px; }
.copy-editor { display: grid; gap: 16px; }
.batch { background: color-mix(in srgb, var(--admin-accent) 5%, var(--color-bg-row)); border-color: color-mix(in srgb, var(--admin-accent) 24%, var(--color-separator)); display: grid; gap: 14px; }
.batch-heading strong { font-size: 14px; }
.batch-fields { display: flex; flex-wrap: wrap; gap: 12px; align-items: end; }
.batch-fields > label { flex: 1 1 140px; }
.emoji-list { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 210px), 1fr)); gap: 16px; list-style: none; padding: 0; margin: 0; }
.emoji-card { display: flex; flex-direction: column; min-width: 0; padding: 14px; border: 1px solid var(--color-separator); border-radius: 16px; background: var(--color-bg-row); transition: border-color .15s, box-shadow .15s; }
.emoji-card:hover { border-color: color-mix(in srgb, var(--admin-accent) 45%, var(--color-separator)); }
.emoji-card--selected { border-color: var(--admin-accent); box-shadow: 0 0 0 1px var(--admin-accent); }
.emoji-card__top { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 12px; }
.emoji-badge, .emoji-flags span { font-size: 10px; border-radius: 5px; padding: 3px 7px; background: var(--color-fill); color: var(--color-label-secondary); }
.emoji-badge--disabled { color: var(--color-danger, #b42318); }
.emoji-card__artwork { width: 100%; height: 112px; display: grid; place-items: center; border: 0; border-radius: 10px; margin-bottom: 14px; cursor: pointer; }
.emoji-card__artwork img { width: 64px; height: 64px; object-fit: contain; }
.emoji-card--disabled .emoji-card__artwork img { opacity: .5; }
.emoji-info { flex: 1; min-width: 0; overflow-wrap: anywhere; }
.emoji-info strong { display: block; font-size: 13px; }
.emoji-info p { margin: 5px 0; font-size: 11px; }
.emoji-flags { display: flex; flex-wrap: wrap; gap: 4px; margin: 8px 0; }
.actions { display: flex; flex-wrap: wrap; gap: 7px; margin-top: 12px; padding-top: 12px; border-top: 1px solid var(--color-separator); }
.actions .admin-button { flex: 1; padding: 7px 9px; font-size: 12px; min-height: 34px; }
.emoji-pagination { display: flex; justify-content: center; align-items: center; flex-wrap: wrap; gap: 14px; }
.emoji-empty { text-align: center; padding: 44px 20px; color: var(--color-label-secondary); }
.emoji-empty h2 { margin: 16px 0 8px; font-size: 16px; color: var(--color-label); }
@media (max-width: 760px) { .emoji-heading { align-items: flex-start; flex-direction: column; } .editor-grid { grid-template-columns: minmax(0, 1fr); gap: 20px; } .editor-artwork { max-width: 100%; } .preview-stage { min-height: 150px; } .editor-fields__pair, .editor-options { grid-template-columns: minmax(0, 1fr); } .emoji-search label, .emoji-search .emoji-search__query { flex-basis: 100%; } .emoji-search > button { width: 100%; } .emoji-pagination span { order: 3; width: 100%; text-align: center; } }
@media (prefers-reduced-motion: reduce) { .emoji-card { transition: none; } }
</style>
