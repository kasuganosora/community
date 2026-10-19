<script setup lang="ts">
import AccessTokenSettings from './AccessTokenSettings.vue'
import WebhookSettings from './WebhookSettings.vue'
import ApiConsoleSettings from './ApiConsoleSettings.vue'
import AuthorizedApps from './AuthorizedApps.vue'
import SettingsSectionBoundary from './SettingsSectionBoundary.vue'
</script>

<template>
  <div class="connections">
    <header class="connections-banner"><t-icon name="link" size="36" aria-hidden="true" /><p>{{ $t('settings.Connections.intro') }}</p></header>
    <SettingsSectionBoundary><AccessTokenSettings /></SettingsSectionBoundary>
    <details class="connection-panel"><summary>{{ $t('settings.SettingsPage.s15') }}</summary><div class="connection-body"><SettingsSectionBoundary><AuthorizedApps /></SettingsSectionBoundary></div></details>
    <details class="connection-panel"><summary>{{ $t('settings.Connections.console') }}</summary><div class="connection-body"><SettingsSectionBoundary><ApiConsoleSettings /></SettingsSectionBoundary></div></details>
    <SettingsSectionBoundary><WebhookSettings /></SettingsSectionBoundary>
  </div>
</template>

<style>
.connections { display: grid; gap: 24px; }
.connections-banner { display: grid; justify-items: center; gap: 12px; padding: 28px 20px; border-radius: 18px; background: linear-gradient(135deg, color-mix(in srgb, var(--color-accent) 16%, var(--color-bg-grouped)), var(--color-bg-grouped)); border: 1px solid var(--color-separator); text-align: center; }
.connections-banner p { margin: 0; color: var(--color-label-secondary); }
.connection-panel { border: 1px solid var(--color-separator); border-radius: 16px; background: var(--color-bg-grouped); overflow: hidden; }
.connection-panel > summary { padding: 18px 20px; cursor: pointer; font-weight: 600; }
.connection-body { padding: 4px 20px 20px; display: grid; gap: 16px; }
.connection-form { display: grid; gap: 18px; }
.connection-form label { display: grid; gap: 8px; font-size: var(--text-subhead); }
.connection-form input:not([type="checkbox"]), .connection-form textarea, .connection-form select, .connection-secret textarea { width: 100%; min-width: 0; padding: 12px 14px; border: 1px solid var(--color-separator); border-radius: 10px; background: var(--color-bg); color: var(--color-label); box-sizing: border-box; font: inherit; }
.connection-form textarea { min-height: 110px; resize: vertical; }
.connection-form input:focus-visible, .connection-form select:focus-visible, .connection-form textarea:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
.connection-actions { display: flex; align-items: center; flex-wrap: wrap; gap: 10px; }
.connection-note { margin: 0; color: var(--color-label-secondary); font-size: var(--text-footnote); line-height: 1.6; }
.connection-secret { display: grid; gap: 12px; padding: 16px; border: 1px solid var(--color-accent); border-radius: 12px; overflow-wrap: anywhere; }
.connection-secret textarea { min-height: 90px; font-family: monospace; }
.connection-list { list-style: none; margin: 0; padding: 0; }
.connection-list > li { padding: 16px 0; display: grid; gap: 12px; border-bottom: 1px solid var(--color-separator); }
.connection-list > li:last-child { border-bottom: 0; }
.connection-row { display: flex; gap: 16px; align-items: center; justify-content: space-between; }
.connection-row > div { min-width: 0; overflow-wrap: anywhere; }
.connection-row strong { display: block; }
.connection-row small { color: var(--color-label-secondary); }
.connection-error { color: var(--color-danger); margin: 0; }
.connection-section-title { margin: 0; font-size: var(--text-title3); }
.connection-toggle { display: flex !important; align-items: center; gap: 12px !important; padding: 8px 0; cursor: pointer; }
.settings-page .connections .connection-toggle input { appearance: none; flex: 0 0 38px; width: 38px; height: 22px; border: 1px solid var(--color-separator); border-radius: 20px; background: var(--color-fill); position: relative; margin: 0; cursor: pointer; }
.settings-page .connections .connection-toggle input::after { content: ''; position: absolute; top: 3px; left: 3px; width: 14px; height: 14px; border-radius: 50%; background: var(--color-label-secondary); transition: transform .15s; }
.settings-page .connections .connection-toggle input:checked { background: var(--color-accent); border-color: var(--color-accent); }
.settings-page .connections .connection-toggle input:checked::after { transform: translateX(16px); background: #fff; }
.settings-page .connections .connection-toggle input:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 3px; }
.connection-form fieldset { padding: 12px 0 0; border: 0; border-top: 1px solid var(--color-separator); margin: 0; }
.connection-form legend { padding: 0 8px 0 0; font-weight: 600; }
@media (prefers-reduced-motion: reduce) { .settings-page .connections .connection-toggle input::after { transition: none; } }
</style>
