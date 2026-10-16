// Tests assert the source-language (zh-CN) copy.
import { config } from '@vue/test-utils'

localStorage.setItem('ui-locale', 'zh-CN')
const { i18n, t } = await import('@/shared/i18n')
config.global.plugins = [...(config.global.plugins ?? []), i18n]
config.global.config.globalProperties = { ...config.global.config.globalProperties, $t: t } as typeof config.global.config.globalProperties
