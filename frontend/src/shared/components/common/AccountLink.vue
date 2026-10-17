<script setup lang="ts">
import { ref } from 'vue'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { accountHover, showAccountHover, leaveAccountHover, closeAccountHover, focusAccountHover, useAccountHoverOwner } from '@/shared/composables/useAccountHover'
const props = defineProps<{ account: MastodonAccount }>()
const root = ref<HTMLElement>()
useAccountHoverOwner(root)
function open(event: Event) {
  if ('pointerType' in event && event.pointerType !== 'mouse') return
  showAccountHover(event.currentTarget as HTMLElement, { acct: props.account.acct, account: props.account })
}
</script>

<template>
  <router-link v-slot="{ href, navigate }" :to="{ name: 'profile', params: { username: account.acct || account.username } }" custom>
    <a ref="root" :href="href" class="account-link" :aria-expanded="accountHover?.anchor === root" aria-haspopup="dialog"
      @pointerenter="open" @pointerleave="leaveAccountHover" @focus="open" @blur="leaveAccountHover"
      @keydown="focusAccountHover" @click.stop="closeAccountHover(); navigate($event)"
>
      <slot />
    </a>
  </router-link>
</template>

<style scoped>
.account-link { color: inherit; text-decoration: none; min-width: 0; }
</style>
