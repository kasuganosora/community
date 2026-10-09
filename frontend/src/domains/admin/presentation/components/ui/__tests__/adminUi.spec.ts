import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { AdminAlert, AdminBadge, AdminButton, AdminCheck, AdminEmpty, AdminField, AdminInlineForm, AdminPageHeader, AdminRow } from '..'

describe('admin UI primitives', () => {
  it('renders a page header with optional description and actions', () => {
    const bare = mount(AdminPageHeader, { props: { title: '标题' } })
    expect(bare.get('h1').text()).toBe('标题')
    expect(bare.find('p').exists()).toBe(false)
    expect(bare.find('.admin-page-header__actions').exists()).toBe(false)
    const full = mount(AdminPageHeader, { props: { title: '标题', description: '说明' }, slots: { default: '<button>刷新</button>' } })
    expect(full.get('p').text()).toBe('说明')
    expect(full.get('.admin-page-header__actions button').text()).toBe('刷新')
  })

  it('buttons default to type=button and block interaction while loading', () => {
    const idle = mount(AdminButton, { slots: { default: '保存' } })
    expect(idle.attributes('type')).toBe('button')
    expect(idle.classes()).toEqual(['admin-button'])
    const busy = mount(AdminButton, { props: { variant: 'danger', size: 'sm', loading: true }, slots: { default: '删除' } })
    expect(busy.classes()).toEqual(expect.arrayContaining(['admin-button--danger', 'admin-button--sm', 'admin-button--loading']))
    expect(busy.attributes('disabled')).toBeDefined()
    expect(busy.attributes('aria-busy')).toBe('true')
    expect(busy.find('.admin-spinner').exists()).toBe(true)
  })

  it('badges and alerts expose semantic tone and roles', () => {
    expect(mount(AdminBadge, { slots: { default: '正常' } }).classes()).toEqual(['admin-badge'])
    expect(mount(AdminBadge, { props: { tone: 'success' }, slots: { default: '已连接' } }).classes()).toContain('admin-badge--success')
    expect(mount(AdminAlert, { props: { tone: 'danger' }, slots: { default: '失败' } }).attributes('role')).toBe('alert')
    const info = mount(AdminAlert, { props: { tone: 'success' }, slots: { default: '完成', action: '<button>撤销</button>' } })
    expect(info.attributes('role')).toBe('status')
    expect(info.get('.admin-alert__action button').text()).toBe('撤销')
  })

  it('fields associate the label above the control and show error over hint', () => {
    const field = mount(AdminField, { props: { label: '名称', hint: '提示' }, slots: { default: '<template #default="{ id }"><input :id="id" /></template>' } })
    const id = field.get('input').attributes('id')
    expect(id).toBeTruthy()
    expect(field.get('label').attributes('for')).toBe(id)
    expect(field.get('.admin-field__hint').text()).toBe('提示')
    const errored = mount(AdminField, { props: { label: '名称', hint: '提示', error: '必填' }, slots: { default: '<template #default="{ id }"><input :id="id" /></template>' } })
    expect(errored.find('.admin-field__hint').exists()).toBe(false)
    expect(errored.get('[role=alert]').text()).toBe('必填')
  })

  it('checkboxes bind v-model and respect disabled', async () => {
    const check = mount(AdminCheck, { props: { label: '启用', modelValue: false, 'onUpdate:modelValue': (v: boolean) => check.setProps({ modelValue: v }) } })
    await check.get('input').setValue(true)
    expect(check.props('modelValue')).toBe(true)
    await check.setProps({ disabled: true })
    expect(check.get('input').attributes('disabled')).toBeDefined()
  })

  it('inline form keeps input and button in one row and emits submit', async () => {
    const form = mount(AdminInlineForm, { props: { label: '地址', buttonLabel: '添加', modelValue: '', placeholder: 'https://', inputmode: 'url' } })
    expect(form.get('label').attributes('for')).toBe(form.get('input').attributes('id'))
    expect(form.get('.admin-inline-form__row input').attributes('inputmode')).toBe('url')
    expect(form.get('.admin-inline-form__row button').text()).toBe('添加')
    await form.get('form').trigger('submit')
    expect(form.emitted('submit')).toHaveLength(1)
    await form.setProps({ busy: true })
    expect(form.get('button').attributes('disabled')).toBeDefined()
    expect(form.get('input').attributes('disabled')).toBeDefined()
  })

  it('rows align title, meta and actions', () => {
    const row = mount(AdminRow, { props: { title: 'https://relay.example/inbox', mono: true }, slots: { meta: '<span>已连接</span>', default: '<button>停用</button>' } })
    expect(row.get('.admin-row__title--mono').text()).toBe('https://relay.example/inbox')
    expect(row.get('.admin-row__meta').text()).toBe('已连接')
    expect(row.get('.admin-row__actions button').text()).toBe('停用')
    expect(mount(AdminRow, { props: { title: 'x' } }).find('.admin-row__actions').exists()).toBe(false)
  })

  it('empty state renders its message', () => {
    expect(mount(AdminEmpty, { slots: { default: '暂无' } }).text()).toBe('暂无')
  })
})
