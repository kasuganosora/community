<script setup lang="ts">
import RelationSection from './RelationSection.vue'
import AccountRelationList from './AccountRelationList.vue'
import DomainBlockSettings from './DomainBlockSettings.vue'
</script>

<template>
  <div class="block-settings">
    <RelationSection icon="swap" :title="$t('settings.Blocks.hiddenReblogs')" testid="section-hidden-reblogs">
      <p class="settings-field__hint block-settings__hint">{{ $t('settings.Blocks.hiddenReblogsHint') }}</p>
      <AccountRelationList kind="hidden_reblogs" />
    </RelationSection>
    <RelationSection icon="browse-off" :title="$t('settings.Blocks.mutes')" testid="section-mutes">
      <p class="settings-field__hint block-settings__hint">{{ $t('settings.Blocks.mutesHint') }}</p>
      <AccountRelationList kind="mutes" />
    </RelationSection>
    <RelationSection icon="stop-circle" :title="$t('settings.Blocks.blocks')" testid="section-blocks">
      <p class="settings-field__hint block-settings__hint">{{ $t('settings.Blocks.blocksHint') }}</p>
      <AccountRelationList kind="blocks" />
    </RelationSection>
    <RelationSection icon="internet" :title="$t('settings.SettingsPage.s17')" testid="section-domains">
      <DomainBlockSettings />
    </RelationSection>
  </div>
</template>

<style scoped>
.block-settings { display: flex; flex-direction: column; gap: .75rem; min-width: 0; }
.block-settings__hint { padding-top: .75rem; }
</style>
