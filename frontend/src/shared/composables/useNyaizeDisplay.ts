import { ref } from 'vue'

/** Viewer preference: show cats' posts nyaized (Misskey: disable cat speech). */
const KEY = 'nyaize-display'

function read(): boolean {
  try { return localStorage.getItem(KEY) !== 'off' } catch { return true }
}

const enabled = ref(read())

export function useNyaizeDisplay() {
  function setEnabled(next: boolean) {
    enabled.value = next
    try { localStorage.setItem(KEY, next ? 'on' : 'off') } catch { /* private mode */ }
  }
  return { enabled, setEnabled }
}
