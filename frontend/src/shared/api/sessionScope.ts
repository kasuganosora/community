import { ref } from 'vue'

/** Changes immediately when credentials change; pending data belongs to the previous session. */
export const sessionEpoch = ref(0)
export function invalidateSessionData() { sessionEpoch.value++ }
