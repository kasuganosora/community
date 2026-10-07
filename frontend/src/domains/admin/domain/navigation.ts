export const adminNavigation = [
  { label: '工作台', items: [
    { path: '/admin', title: '概览', description: '了解社区近况，处理需要关注的事务。', icon: 'dashboard' },
  ] },
  { label: '社区管理', items: [
    { path: '/admin/accounts', title: '用户管理', description: '查找成员、审核注册申请与管理账户状态。', icon: 'usergroup' },
    { path: '/admin/reports', title: '举报与内容', description: '查看举报上下文，跟进处理结果。', icon: 'flag' },
    { path: '/admin/emojis', title: '自定义表情', description: '让社区有自己的表达方式。', icon: 'happy' },
  ] },
  { label: '联邦与服务', items: [
    { path: '/admin/federation', title: '联邦中继', description: '管理 ActivityPub 中继连接。', icon: 'internet' },
    { path: '/admin/system', title: 'Matrix 服务', description: '查看消息服务状态与连接诊断。', icon: 'server' },
  ] },
  { label: '站点维护', items: [
    { path: '/admin/settings', title: '站点设置', description: '维护社区规则与验证码配置。', icon: 'setting' },
    { path: '/admin/migrations', title: '数据迁移', description: '预检并导入同域名的完整站点备份。', icon: 'data' },
  ] },
]
export function adminSection(path: string) {
  return adminNavigation.flatMap(group => group.items).find(item => item.path === path.replace(/\/$/, '')) ?? adminNavigation[0]!.items[0]!
}
