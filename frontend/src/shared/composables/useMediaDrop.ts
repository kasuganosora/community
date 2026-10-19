import { onBeforeUnmount, onMounted, ref, watch, type Ref } from 'vue'

export interface MediaDropOptions {
  /** Receives the accepted files of one paste or drop, in order. */
  onFiles: (files: File[]) => void
  /** Return false while the composer cannot take attachments (posting, editing, full). */
  enabled?: () => boolean
  /** Which files a paste/drop may attach. Default: images only. */
  accept?: (file: File) => boolean
}

export const acceptImages = (file: File) => file.type.startsWith('image/')
export const acceptMedia = (file: File) => /^(image|video|audio)\//.test(file.type)
export const acceptAny = () => true

/** Files carried by a clipboard or drag payload (items first: some browsers leave `files` empty on paste). */
export function transferFiles(data: DataTransfer | null | undefined, accept: (file: File) => boolean = acceptImages): File[] {
  if (!data) return []
  const out: File[] = []
  for (const item of Array.from(data.items ?? [])) {
    if (item.kind !== 'file') continue
    const file = item.getAsFile()
    if (file && accept(file)) out.push(file)
  }
  if (!out.length) for (const file of Array.from(data.files ?? [])) if (accept(file)) out.push(file)
  return out
}

function carriesFiles(data: DataTransfer | null | undefined) {
  return !!data && Array.from(data.types ?? []).includes('Files')
}

/**
 * Paste (Ctrl/Cmd+V) and drag-and-drop attachment for every composer.
 * - A paste with images uploads them; if it also carries text the text is
 *   inserted as usual, otherwise the default paste is cancelled.
 * - Plain text pastes are untouched.
 * - Dropping files on the target attaches them; `dragging` drives the
 *   drop-zone highlight.
 */
export function useMediaDrop(target: Ref<HTMLElement | null | undefined>, options: MediaDropOptions) {
  const dragging = ref(false)
  const accept = options.accept ?? acceptImages
  const enabled = () => options.enabled?.() ?? true
  let depth = 0

  function onPaste(event: ClipboardEvent) {
    if (!enabled()) return
    const files = transferFiles(event.clipboardData, accept)
    if (!files.length) return
    const text = event.clipboardData?.getData('text/plain') ?? ''
    if (!text) event.preventDefault()
    options.onFiles(files)
  }
  function onDragEnter(event: DragEvent) {
    if (!carriesFiles(event.dataTransfer) || !enabled()) return
    event.preventDefault()
    depth++
    dragging.value = true
  }
  function onDragOver(event: DragEvent) {
    if (!carriesFiles(event.dataTransfer) || !enabled()) return
    event.preventDefault()
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy'
    dragging.value = true
  }
  function onDragLeave() {
    depth = Math.max(0, depth - 1)
    if (!depth) dragging.value = false
  }
  function onDrop(event: DragEvent) {
    depth = 0
    dragging.value = false
    if (!carriesFiles(event.dataTransfer)) return
    event.preventDefault()
    if (!enabled()) return
    const files = transferFiles(event.dataTransfer, accept)
    if (files.length) options.onFiles(files)
  }
  const listeners: [string, EventListener][] = [
    ['paste', onPaste as EventListener], ['dragenter', onDragEnter as EventListener], ['dragover', onDragOver as EventListener],
    ['dragleave', onDragLeave as EventListener], ['drop', onDrop as EventListener],
  ]
  let bound: HTMLElement | null = null
  function unbind() { if (bound) for (const [name, fn] of listeners) bound.removeEventListener(name, fn); bound = null }
  function bind(el: HTMLElement | null | undefined) {
    if (el === bound) return
    unbind()
    if (!el) return
    for (const [name, fn] of listeners) el.addEventListener(name, fn)
    bound = el
  }
  watch(target, bind, { flush: 'post' })
  onMounted(() => bind(target.value))
  onBeforeUnmount(unbind)
  return { dragging }
}
