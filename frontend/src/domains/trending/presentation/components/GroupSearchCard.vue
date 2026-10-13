<script setup lang="ts">
import { computed } from 'vue'
import { groupPath, type Group } from '@/domains/message/domain/group'
import GroupAvatar from '@/domains/message/presentation/components/GroupAvatar.vue'
import { groupClientLink, groupMembershipLabel } from '../../domain/groupSearch'

const props = defineProps<{ group: Group; target?: string; via?: string[]; source?: string }>()
const name = computed(() => props.group.name || '未命名群组')
const clientLink = computed(() => groupClientLink(props.group.client_url))
const membership = computed(() => groupMembershipLabel(props.group.membership))
const previewLink = computed(() => ({ path: '/messages/groups', query: { join: props.target || props.group.target || props.group.id, via: props.via || props.group.via, preview: props.group.preview_ref } }))
const joinedLink = computed(() => groupPath(props.group.id))
const action = computed(() => {
  if (props.group.encryption === 'encrypted') return '查看群组'
  if (props.group.membership === 'invite') return '查看邀请'
  if (props.group.membership === 'knock') return '查看申请'
  if (props.group.join_rule === 'knock' || props.group.join_rule === 'knock_restricted') return '查看并申请'
  if (props.group.join_rule === 'public') return '查看并加入'
  return '查看群组'
})
</script>

<template>
  <article class="group-result" :aria-label="name">
    <GroupAvatar :name="name" :url="group.avatar_url" :size="44" />
    <div class="group-result__content">
      <h3>{{ name }}</h3>
      <p v-if="group.topic" class="group-result__topic">{{ group.topic }}</p>
      <div class="group-result__meta">
        <span v-if="membership">{{ membership }}</span>
        <span>{{ typeof group.member_count === 'number' && group.member_count >= 0 ? `${group.member_count.toLocaleString()} 位成员` : '成员数未知' }}</span>
        <span v-if="group.encryption === 'encrypted'">端到端加密 · 请使用客户端</span>
        <span v-else-if="group.encryption !== 'unencrypted'">加密状态待确认</span>
        <span v-else>非端到端加密</span>
      </div>
      <p v-if="source" class="group-result__source">{{ source }}</p>
      <div class="group-result__actions">
        <router-link v-if="group.membership === 'join'" class="btn btn--secondary btn--sm" :to="joinedLink">
          {{ group.encryption === 'encrypted' ? '查看群组' : '打开群聊' }}
        </router-link>
        <router-link v-else-if="group.membership !== 'ban'" class="btn btn--secondary btn--sm" :to="previewLink">{{ action }}</router-link>
        <a v-if="clientLink && (group.encryption === 'encrypted' || group.membership === 'ban')" class="group-result__client" :href="clientLink" target="_blank" rel="noopener noreferrer">使用客户端<span class="sr-only">（在新窗口打开）</span></a>
      </div>
    </div>
  </article>
</template>

<style scoped>
.group-result { display: flex; align-items: flex-start; gap: 0.875rem; padding: 1rem; border: 1px solid var(--color-separator); border-radius: var(--radius-lg); background: var(--color-bg-row); }
.group-result__content { flex: 1; min-width: 0; }
.group-result h3 { margin: 0; font-size: 1rem; font-weight: 600; line-height: 1.4; overflow-wrap: anywhere; }
.group-result__topic { margin: 0.375rem 0 0; font-size: 0.9375rem; line-height: 1.5; overflow-wrap: anywhere; white-space: pre-line; display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; }
.group-result__meta { display: flex; gap: 0.25rem 0.75rem; flex-wrap: wrap; margin-top: 0.375rem; color: var(--color-label-secondary); font-size: 0.8125rem; line-height: 1.5; }
.group-result__source { color: var(--color-label-secondary); font-size: 0.8125rem; margin: 0.25rem 0 0; overflow-wrap: anywhere; }
.group-result__actions { display: flex; flex-wrap: wrap; align-items: center; gap: 0.5rem 1rem; margin-top: 0.75rem; }
.group-result__actions .btn, .group-result__client { min-height: 2.75rem; }
.group-result__client { display: inline-flex; align-items: center; color: var(--color-accent); font-size: 0.875rem; }
.group-result__actions :is(a, button):focus-visible { outline: 2px solid var(--color-focus-ring); outline-offset: 3px; }
@media (max-width: 360px) { .group-result { padding: 0.75rem; gap: 0.625rem; } }
</style>
