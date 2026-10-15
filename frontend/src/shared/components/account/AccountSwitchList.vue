<script setup lang="ts">
/** Misskey-style account menu body: 个人资料 / other accounts / 添加账户 / 管理账户. */
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { useUiChrome } from '@/shared/composables/useUiChrome'
import { asAvatarAccount, useAccountActions } from './useAccountActions'

const emit = defineEmits<{ close: [] }>()
const router = useRouter()
const auth = useAuthStore()
const { openAccountsManager } = useUiChrome()
const { addAccount, switchTo } = useAccountActions(() => emit('close'))

const others = computed(() => (auth.accounts ?? []).filter(a => a.id !== auth.currentUser?.id))

function profile() {
  emit('close')
  if (auth.currentUser) router.push(`/@${auth.currentUser.username}`).catch(() => {})
}
function manage() {
  emit('close')
  openAccountsManager()
}
</script>

<template>
  <div v-if="auth.currentUser" class="acct-menu" role="menu" aria-label="账户">
    <button type="button" role="menuitem" class="acct-menu__row acct-menu__me" data-qa="acct-profile" @click="profile">
      <UserAvatar :account="auth.currentUser" :size="36" />
      <span class="acct-menu__text">
        <span class="acct-menu__name"><EmojiText :text="auth.currentUser.display_name || auth.currentUser.username" :emojis="auth.currentUser.emojis" /></span>
        <span class="acct-menu__sub">个人资料</span>
      </span>
      <t-icon name="check" :size="18" class="acct-menu__check" aria-label="当前账户" />
    </button>
    <div class="acct-menu__divider" role="separator" />
    <button
      v-for="a in others" :key="a.id" type="button" role="menuitem"
      class="acct-menu__row" :data-qa="`acct-switch-${a.username}`"
      @click="switchTo(a)"
    >
      <UserAvatar :account="asAvatarAccount(a)" :size="32" />
      <span class="acct-menu__text">
        <span class="acct-menu__name">{{ a.display_name || a.username }}</span>
        <span class="acct-menu__sub">@{{ a.username }}<template v-if="a.needsRelogin"> · 需要重新登录</template></span>
      </span>
    </button>
    <button type="button" role="menuitem" class="acct-menu__row acct-menu__action" data-qa="acct-add" @click="addAccount()">
      <t-icon name="user-add" :size="20" /><span>添加账户</span>
    </button>
    <button type="button" role="menuitem" class="acct-menu__row acct-menu__action" data-qa="acct-manage" @click="manage">
      <t-icon name="user-list" :size="20" /><span>管理账户</span>
    </button>
  </div>
</template>

<style scoped>
.acct-menu { display: flex; flex-direction: column; min-width: 240px; padding: var(--space-1) 0; }
.acct-menu__row {
  display: flex; align-items: center; gap: var(--space-3); width: 100%;
  min-height: var(--hit-target, 44px); padding: var(--space-2) var(--space-3);
  text-align: left; color: var(--color-label); border-radius: var(--radius-md, 8px);
  transition: background-color var(--duration-fast) var(--ease-out);
}
.acct-menu__row:hover, .acct-menu__row:focus-visible { background: var(--color-hover, var(--color-pressed)); }
.acct-menu__row:active { background: var(--color-pressed); }
.acct-menu__text { flex: 1; min-width: 0; display: flex; flex-direction: column; }
.acct-menu__name { font-weight: var(--weight-semibold); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.acct-menu__sub { font-size: var(--text-footnote); color: var(--color-label-secondary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.acct-menu__check { color: var(--color-accent); flex-shrink: 0; }
.acct-menu__divider { height: var(--hairline, 1px); margin: var(--space-1) var(--space-3); background: var(--color-separator); }
.acct-menu__action { color: var(--color-label-secondary); }
.acct-menu__action span { color: var(--color-label); }
</style>
