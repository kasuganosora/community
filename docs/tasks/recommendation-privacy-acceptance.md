# 推荐系统隐私验收

日期：2026-10-14。范围：当前 P0/P1 实现的推荐时间线、推荐关注、热点话题/链接、个人反馈、站级禁推和 Meilisearch 专用索引。此文档由独立验收工作线维护；计划中的项目不等于已通过。

## 验收环境与原则

- 使用独立 SQLite 测试夹具、隔离开发 API 与专门的 Meilisearch 测试索引，不访问线上数据库或真实用户内容。
- 所有测试内容使用唯一隐私标记。检查响应正文、嵌套引用、媒体/链接/投票字段和索引文档；不能只检查根帖 ID。
- 在缓存和分页快照生成后撤回权限，下一次请求必须使用当前 SQL 权限。已经发送给客户端的数据无法远程抹除。
- 单元/接口测试、实际运行 API、静态代码审查分别报告；未运行真实向量服务不能写为向量服务验收通过。
- 测试报告不记录 Cookie、访问令牌、密码、索引密钥或真实私人数据。测试产物放在 `/tmp/community-discovery-qa`。

## 威胁模型

需防止匿名或其他登录用户通过推荐响应、计数、链接预览、引用、缓存游标、反馈接口差异或搜索索引，得到其不应被主动展示的内容、私人选择或隐藏关系。Redis 和 Meilisearch 是服务端基础设施，不被视为授权来源；缓存中的资格不能覆盖数据库的当前状态。管理员仅能在其授权的后台能力范围内管理站级规则。

`local_only` 延续现有项目语义：不联邦传播，本站公开浏览仍可读；它不是仅登录可见或私信。验收应验证远端身份不能收到该内容，并检查推荐没有新增向 ActivityPub/Matrix 投递的路径。

## 可见性和撤回矩阵

| 输入/变化 | 时间线、热点、链接、索引要求 | 编号 |
| --- | --- | --- |
| 合格作者最近 7 天的 public 原帖 | 可参与；仍执行个人过滤 | P01 |
| unlisted/private/direct，即使浏览者本来有读取权 | 不主动推荐；独有标签/链接不进入热点 | P02 |
| 删除、定时、历史回填、未来日期、超出窗口、转发壳、回复 | 根候选排除 | P03 |
| 本站 discoverable=false 或 noindex=true | 当前读取立即排除 | P04 |
| 远端明确拒绝 discoverable、indexable=false、无效的偏好更新、未来检查时间或上次明确允许超过 30 天 | 排除；收到普通帖子不续期；后台旧响应不能覆盖新收到的隐私变更 | P05 |
| 远端已验证明确允许，检查在 24 小时内 / 24 小时至 30 天 | 前者使用当前验证结果，后者允许使用上次明确允许的结果回退；任何已收到的拒绝优先 | P05 |
| 从未读取过远端发现偏好，且没有 noindex/indexable=false | 仅允许参与话题汇总；不能据此推荐个人帖子、账号或写入推荐专用 Meili 索引 | P05 |
| 引用链中的任何对象不合格、缺失、循环、深度超限 | 整张根卡排除；不得只删除引用字段而保留其旁路内容 | P06 |
| 双向屏蔽、站级/个人域限制、有效静音、对帖静音 | 当前读取排除；过期静音按原规则恢复 | P07 |
| home/public hide | 对应上下文服务端排除；不误用另一上下文 | P08 |
| warn/CW/敏感媒体 | 返回正确的警告元数据；不得当作无警告普通卡片 | P09 |
| 用户反馈或站级禁推发生在缓存/快照生成之后 | 后续请求撤回；规则撤销互不覆盖 | P10 |
| 资格、反馈或 SQL 读取故障 | 失败关闭，不发送缓存正文；Redis 故障回退同资格 | P11 |

## 身份、接口与数据最小化

| 验收项 | 通过标准 | 编号 |
| --- | --- | --- |
| 用户 off | 推荐时间线禁用、建议为空；不读取私人排序来源 | P12 |
| 用户 generic | 不读取关注话题或个人关系用于排名；必要的屏蔽/已关注排除仍允许 | P13 |
| 私信/私有帖/收藏/Matrix 群消息、成员、已读 | 不作为公共候选、画像、公开计数或 Meili 查询 | P14 |
| 隐藏的关注集合 | 不成为二跳推荐和社交召回证据 | P15 |
| 用户 A 游标交给 B、篡改游标、旧偏好版本 | 拒绝或 reset_required，不发送 A 的结果/理由 | P16 |
| 反馈列表、创建与删除 | 不能冒充 owner、写站级规则或读取/删除别人反馈 | P17 |
| 私密对象与不存在 ID 的反馈 | 不借响应差异提供对象存在性查询能力 | P18 |
| OAuth scope、无效 token、匿名访问 | 精确执行 scope；无效 token 不降级匿名；管理接口要求相应权限 | P19 |
| HTTP 缓存 | 内容与个人资格相关响应 Cache-Control: no-store | P20 |
| Meili 索引/调用 | 仅专用公开资格文档；不复制通用搜索索引，不发送私人兴趣/关系；浏览器不获得密钥 | P21 |
| 页面换账号、反馈、偏好修改 | 清空旧推荐窗口及相关缓存；不重显上一账号数据 | P22 |

## 执行方法与发布条件

自动验收文件以 `privacy_acceptance` 命名，补充已有资格、Actor、路由、趋势、序列化测试，避免通过复制实现来证明自身。真实 API 使用两个不同测试账号验证反馈和分页；私密帖子使用独立标记，状态、热点和链接响应均需确认未出现标记。Meili 验证同时检查索引输入提取和实际引擎文档；私有/引用私有等样本必须存在于源数据库，避免空夹具假通过。

关键隐私项 P02、P04–P08、P10–P11、P14–P21 出现失败即阻止推荐启用；由对应 owner 修复后重跑原失败用例。明确记录未验证内容、外部变更尚未到达本站等边界，不将静态审查等同运行证据。

## 执行结果

以下为初次实现的验收记录：**后端定向测试 99 项（含子用例）、前端隐私相关测试 16 项通过；真实 SQLite、MySQL 和 PostgreSQL API 各 29/29 项通过**。真实 Meilisearch 文档读取验证通过。随后 `0b99836..87d26be` 修改了 P05 的远端回退及话题汇总策略，上表已按当前实现更新；初次记录不能代替该策略的复验，新的检查范围见 [提交审查记录](commit-review-20261014.md)。文末列出的部署/外部服务范围仍未验证。

| 范围 | 已执行证据 | 状态 |
| --- | --- | --- |
| P01–P08、P10–P11 | `models/recommendation_privacy_acceptance_test.go`、既有 `recommendation_test.go`、`account_discovery_test.go`；使用旧内存模型加当前 SQL 撤回矩阵 | 通过 |
| P05 的三态、刷新、过期、增量 Update | `activitypub/discovery_preferences_test.go`；完整/部分文档及失败不续期 | 通过 |
| P06/P09 引用整卡、warn 上下文 | 新隐私嵌套引用测试及 `serializer/recommendation_quote_test.go` | 通过 |
| P10/P16 旧快照、策略撤销、用户绑定 | `discovery/privacy_acceptance_test.go`；先生成窗口再私信化、作者拒绝、站级禁推，另一用户不能复用游标；停用再启用不复活 | 通过 |
| P12–P14 generic/off 与公开互动 | 故意删除私人排序来源表，generic 仍成功、personalized 对照失败；off 在内容表不可用时仍禁用。派生话题测试包含私密、禁推、隐藏关键词、私有引用、其他用户和过期互动 | 通过 |
| P15 隐藏关系、P14 Matrix/收藏不画像 | 静态审查 `feedSocialIDs`、`SuggestAccounts`、`feedInterestTopics` 和 provider 参数：隐藏关注集合不进入图召回，候选/画像路径不读 Matrix 或收藏；没有构造真实 Matrix 交通做黑盒证明 | 静态通过，黑盒范围受限 |
| P17–P19 用户/站级权限、审计 | `discovery_routes_test.go`、`controllers/api/admin/discovery_test.go`；owner/scope 伪造、跨用户删除、OAuth scope、审计失败回滚 | 通过 |
| P02/P16–P20 真实 API | 2026-10-13 23:47 UTC 对隔离 MySQL `18111`、PostgreSQL `18112` 服务逐一跑 29 项；新建 private/direct/unlisted 标记帖后核对五个推荐/热点接口，跨账号游标拒绝、非法 token 拒绝、owner 隔离、off、管理员边界和 no-store 均通过；已恢复偏好、删除本轮夹具 | 各 29/29 通过 |
| P21 索引文档 | `TestPrivacyAcceptanceMeiliExtractionDoesNotExportPrivateObjects` 和 `TestPrivacyAcceptanceRealMeiliContainsOnlyPublicDocument`；真实 Meilisearch v1.55.0 新建唯一测试代，直接读取 documents 确认只有 public 控制帖，私密三类及私有引用未进入；测试代随后回收 | 通过 |
| P22 账号切换与反馈 | `useDiscoveryFeed.spec.ts`、`DiscoveryTags.spec.ts`、`DiscoverySettings.spec.ts`；换账号/偏好时丢弃迟到响应；反馈撤回引用与转发中的目标 | 通过，组件测试而非真实浏览器会话切换 |

发现并修复的问题：

1. **反馈接口泄露私密对象是否存在。** 旧代码对用户级 deny 规则检查目标是否存在，已存在的私信 ID 与不存在 ID 返回不同状态。模型层已改成个人规则仅验证 key 语法、不返回目标信息；站级治理另保留对象校验。实际隔离 API 首轮确认了旧二进制的 200/400 差异；更新后二者在 MySQL、PostgreSQL API 均返回 200，规则内容只包含调用者提交的 key。
2. **派生兴趣跳过 hide 关键词。** `feedInterestTopics` 原先使用 metadata-only 资格，已隐藏的历史互动仍会产生话题兴趣。已改为最多 100 条完整资格检查；包含关键词和私有引用的独立用例通过。
3. **网页反馈仅撤回根卡。** 隐藏作者/帖子后，同页其他卡片引用该对象时仍显示。已改为检查整条有界 quote/reblog 链，循环或无法确认时保守移除；两个反馈类型的回归通过。

复现命令（仓库根目录；测试数据库环境变量应指向隔离数据库）：

```sh
source /workspace/.community-setup/activate.sh
export GOTMPDIR=/tmp GOMAXPROCS=2
DISCOVERY_TEST_MEILI_URL=http://127.0.0.1:17700 go test -json -p 1 \
  ./models ./discovery ./activitypub ./controllers/api/trends \
  ./controllers/api/account ./controllers/api/admin ./serializer . \
  -run 'TestPrivacyAcceptance|TestRecommendation|TestDiscoveryRoutes|TestDiscoveryGovernance|TestAccountAllowsRecommendation|TestActorDiscovery|TestRemoteDiscovery|TestLocalActorPublishes|TestTag.*|TestLinksRequireCurrentPublicSource|TestTrendsSQLFailureDoesNotReturnCachedData|TestMeiliExtraction|TestFeedSignedCursor|TestFeedPaging' \
  -count=1
cd frontend
npm test -- src/domains/discovery/presentation/composables/__tests__/useDiscoveryFeed.spec.ts \
  src/domains/discovery/presentation/components/__tests__/DiscoveryTags.spec.ts \
  src/domains/discovery/presentation/components/__tests__/DiscoverySettings.spec.ts
```

本次执行记录保存在 `/tmp/community-discovery-qa/privacy/go-results.jsonl` 和 `frontend-results.log`，包含测试名与状态，不包含凭据。真实 HTTP 检查脚本为同目录 `api_acceptance.py`，从权限受限的 fixture 文件读取账号，输出仅测试结果，完成后恢复偏好并删除自己创建的内容。

真实 API 复验命令与记录：

```sh
python3 /tmp/community-discovery-qa/privacy/api_acceptance.py \
  http://127.0.0.1:18111 /tmp/community-discovery-qa/mysql/fixture.private.json
python3 /tmp/community-discovery-qa/privacy/api_acceptance.py \
  http://127.0.0.1:18112 /tmp/community-discovery-qa/postgres/fixture.private.json
```

结果分别存为 `api-mysql-results.json` 和 `api-postgres-results.json`。两套服务是独立实验数据库，端口与 fixture 路径仅供本次复现，不能替换为线上地址执行。

整合验收随后在最终 SQLite 新库服务 `18110` 重跑同一真实 API 脚本，亦为 29/29 通过，记录为 `api-sqlite-results.json`；该次使用 `/tmp/community-discovery-qa/sqlite-fresh/fixture.private.json`，仍未访问生产数据。

未验证及边界：没有连接外部语义模型，故不声明第三方模型服务的日志/保留策略已验收；生产 CDN/反向代理是否覆盖 `no-store` 需部署时另查；尚未到达本站的远端隐私变更不在“下一请求立即撤回”的保证内；此前已合法发送的页面/截图无法远程抹除。此报告不证明线上数据从未泄漏，也不宣称推荐质量或大型数据集性能已达标。
