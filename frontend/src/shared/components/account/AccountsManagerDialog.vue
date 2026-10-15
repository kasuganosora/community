<script setup lang="ts">
/** 管理账户: every account signed in on this device, each removable on its own. */
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { useUiChrome } from '@/shared/composables/useUiChrome'
import { asAvatarAccount, useAccountActions } from './useAccountActions'

const auth = useAuthStore()
const { accountsManagerOpen } = useUiChrome()
const close = () => { accountsManagerOpen.value = false }
const { addAccount, switchTo, signOut } = useAccountActions(close)
</script>

<template>
  <t-dialog v-model:visible="accountsManagerOpen" header="管理账户" :footer="false" width="min(440px, 92vw)" attach="body" @opened="auth.refreshAccounts()">
    <ul class="acct-mgr" data-qa="acct-manager">
      <li v-for="a in auth.accounts" :key="a.id" class="acct-mgr__row" :data-qa="`acct-row-${a.username}`">
        <button type="button" class="acct-mgr__who" :disabled="a.id === auth.currentUser?.id" @click="switchTo(a)">
          <UserAvatar :account="asAvatarAccount(a)" :size="40" />
          <span class="acct-mgr__text">
            <span class="acct-mgr__name">{{ a.display_name || a.username }}</span>
            <span class="acct-mgr__sub">
              @{{ a.username }}
              <span v-if="a.id === auth.currentUser?.id" class="acct-mgr__tag">当前</span>
              <span v-else-if="a.needsRelogin" class="acct-mgr__tag acct-mgr__tag--warn">需要重新登录</span>
            </span>
          </span>
        </button>
        <button v-if="a.needsRelogin" type="button" class="btn btn--sm" @click="addAccount(a.username)">重新登录</button>
        <button type="button" class="btn btn--sm acct-mgr__out" :data-qa="`acct-logout-${a.username}`" @click="signOut(a)">退出</button>
      </li>
    </ul>
    <button type="button" class="btn btn--primary acct-mgr__add" data-qa="acct-mgr-add" @click="addAccount()">
      <t-icon name="user-add" :size="18" /> 添加账户
    </button>
  </t-dialog>
</template>

<style scoped>
.acct-mgr { list-style: none; margin: 0 0 var(--space-4); padding: 0; display: flex; flex-direction: column; gap: var(--space-1); }
.acct-mgr__row { display: flex; align-items: center; gap: var(--space-2); }
.acct-mgr__who { flex: 1; min-width: 0; display: flex; align-items: center; gap: var(--space-3); padding: var(--space-2); border-radius: var(--radius-md, 8px); text-align: left; color: var(--color-label); }
.acct-mgr__who:not(:disabled):hover { background: var(--color-hover, var(--color-pressed)); }
.acct-mgr__who:disabled { cursor: default; opacity: 1; }
.acct-mgr__text { min-width: 0; display: flex; flex-direction: column; }
.acct-mgr__name { font-weight: var(--weight-semibold); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.acct-mgr__sub { font-size: var(--text-footnote); color: var(--color-label-secondary); }
.acct-mgr__tag { margin-left: var(--space-1); padding: 0 6px; border-radius: 999px; background: var(--color-accent); color: #fff; font-size: 11px; }
.acct-mgr__tag--warn { background: var(--color-warning, #ff9500); }
.acct-mgr__out { color: var(--color-danger); }
.acct-mgr__add { width: 100%; justify-content: center; gap: var(--space-2); display: inline-flex; align-items: center; }
</style>
