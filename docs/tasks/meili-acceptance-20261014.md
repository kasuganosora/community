# Meilisearch 推荐专项验收（2026-10-14）

本计划在补充测试执行前固定验收门槛。对象是 Community 的公开推荐独立索引和候选提供器，不是全站搜索，也不改变生产推荐开关或审批版本。测试使用独立索引前缀及测试数据库，不收录生产私信、Matrix 聊天或私人兴趣。

## 验收门槛

| 编号 | 必须证明的行为 | 证据与通过条件 |
| --- | --- | --- |
| M01 | 配置、预算与审批分离 | 缺模型/配置/明确预算时不能启用语义；能力就绪但版本未批准时不发布增强候选；真实词法请求无 hybrid |
| M02 | 审批与实际版本绑定 | 服务版本、模型名/版本/配置改变不继承旧审批；查询期间撤销审批不发布已取得结果 |
| M03 | 公开数据最小化 | private/direct/unlisted、私密引用、删除、作者或远端许可拒绝/过期均不进入新索引；仅 ID 用于候选读取；SQL 当前资格始终覆盖旧索引 |
| M04 | 语义预算为部署共享 | 两个独立提供器共用预算；并发不超额；失败不退款；按 Redis 时钟换小时；Redis 不可用时拒绝语义工作 |
| M05 | 机密不进入诊断 | 对外错误和状态不包含原始模型配置、API Key、响应正文或查询；HTTP 重定向不携带凭据向其他端点请求 |
| M06 | 生命周期与并发有界 | 等待异步任务完成才发布；失租不发布；重启/Redis 状态丢失后回收自身孤儿索引，不删除其他前缀；正常及恢复过程最多两个自身索引 |
| M07 | 故障与降级真实可见 | 引擎/模型失败、过期、超预算时不能假称语义成功；保留规则回退；故障诊断不误报增强已就绪；请求路径不阻塞等待外部模型 |
| M08 | 真实语义能力 | 使用明确记录版本的真实多语模型和真实 Meilisearch 完成文档及查询 embedding，输出实际 semantic 候选；停模型、恢复、撤回与模型版本变更实际验证 |
| M09 | 相关性结论有范围 | 预先记录独立 agent 创作/标注的受控语料、分组、指标和评价方法；如实报告词法/混合差异及失败项，不将代理标注夹具当成生产用户的人工相关性评估，更不推断点击率提升 |

M01–M08 任一隐私违规、错误发布、超预算或无限等待均阻断该能力验收。M09 的生产发布门槛仍沿用[研究计划](community-recommendation-research.md)：真实目标用户/主要语言评价、配对置信区间及退化界限。受控语料不足以满足这个门槛时保持生产审批关闭，不能通过降低标准来声称自动增强已经获准。

## 已有覆盖和本次新增

已有 `discovery/meili_provider_test.go` 覆盖词法真实索引、公开提取、原始字节/标签/磁盘预算、小时失败计费、异步失败、查询批次原子性、失租和数据年龄。`discovery/privacy_acceptance_test.go` 直接检查真实索引文档，`models` 资格与 feed 旧窗口测试检查当前 SQL 授权。此前真实语义模型尚未连接，因此这部分不算已有通过。

本次独立补充测试位于 `discovery/meili_acceptance_test.go`，专门验证配置矩阵、审批撤销、跨提供器预算与时钟、脱敏和真实索引生命周期/远端撤回。真实模型及相关性结果由专项运行补充记录，并区分真实服务、协议模拟和静态审查。

## 执行记录

### 独立能力、隐私和恢复验收

2026-10-14（北京时间），在 `/tmp/community` 使用真实 Meilisearch 1.55.0、独立 Redis 和 SQLite 数据库执行：

```sh
source /workspace/.community-setup/activate.sh
GOTMPDIR=/tmp GOMAXPROCS=2 \
  DISCOVERY_TEST_MEILI_URL=http://127.0.0.1:17700 \
  DISCOVERY_TEST_REDIS_ADDR=127.0.0.1:17779 \
  go test -p 1 ./discovery -run TestMeiliAcceptance -count=1 -v
```

**12 个顶层测试及 20 个子用例全部通过（32 项，3.056 秒）。** 每次使用随机索引前缀，真实 Redis 测试只操作自己的预算键，不清空服务；真实引擎测试退出时回收自己的索引。执行日志位于环境 `/tmp/meili-acceptance-review-final.log`，日志未包含模型凭据或生产数据。

随后对相同 32 项顺序进行真实 MySQL 与 PostgreSQL 回归，仍连接真实 Meili 和 Redis：MySQL 32/32 通过（4.502 秒），PostgreSQL 32/32 通过（3.802 秒）。使用独立 `community_discovery_qa_tests` 数据库，未操作 QA 应用数据库，更未操作生产数据库。命令通过已有 `/tmp/community-discovery-qa/run.py test --dialect mysql|postgres -- ./discovery -run TestMeiliAcceptance -count=1 -v` 绑定凭据，结果保存在 `/tmp/meili-acceptance-mysql.log` 与 `/tmp/meili-acceptance-postgres.log`。

| 验收点 | 本次实际证据 |
| --- | --- |
| M01、M02 | 六项配置逐个缺失及五类非法值不启用语义；模型名、模型版本、模型配置变化或移除模型都拒绝旧审批；搜索执行中撤销审批丢弃整批结果 |
| M03 | 真实索引含本地与明确允许的远端正例；远端撤回后，重建前 SQL 已拒绝旧候选，重建后真实索引不再含该作者。原私密/私密引用真实索引验收继续保留于 `privacy_acceptance_test.go` |
| M04 | 两个独立 Redis 客户端并发 24 次请求，真实 Redis 精确仅允许 10 单位；模拟 Redis 时钟跨小时重置，无退款，Redis 中断拒绝。语义请求实际收到 503 后，第二次不会再次调用模拟模型入口 |
| M05 | HTTP 错误响应含测试机密与查询标记时，对外错误/状态不含标记；307 重定向目标没有收到任何请求，更未收到 API Key |
| M06 | 真实引擎新 provider 恢复共享 manifest，丢失 manifest 后回收自身孤儿索引，再建代次只余一个自身索引；其他搜索索引完整保留。真实故障竞争见下方；完整多进程模型压力运行由联合验收另行记录 |
| M07 | 真实引擎前注入查询及构建 503，仍新鲜的已审核索引保留 `ready`，两种失败通过独立字段可见；新 provider 读到相同故障，成功查询只清除查询失败、成功构建再清除构建失败 |

本次独立测试复现并促成修复的两个问题：

1. 模型或部署连接配置改变后，旧推荐池和游标未失效。修复前模型版本、配置、主机、索引前缀、启用状态五类变更均可继续使用旧游标；修复后旧游标要求刷新，旧增强池生成的新页回到规则模式。测试实际调用 `StatusPage`，并断言整个读取没有外部网络请求。
2. 查询/构建失败被 `Status` 的 `ready` 覆盖，后台看不到故障。现保留部署共享的 `last_query_error`、`last_build_error` 和时间。测试把旧查询阻塞后模拟租约过期，让新副本记录失败，再释放旧查询，确认旧副本既不能清除也不能改写新错误时间。

修复前失败日志为 `/tmp/meili-acceptance-regressions-before.log`；上述最终命令已在修复后复验通过。

### 本阶段结论和边界

- M01–M07 在上表明确的测试规模及故障注入范围内通过。进程替换为独立 provider 实例，共享真实 Redis 的并发配额另行实测；不把它等同于完整集群长期压测。小时边界使用可控 Redis 时钟模拟，真实 Redis 测试证明同小时原子共享。
- M08 尚受真实模型下载网络权限阻塞，不能将协议模拟或未执行测试写成真实语义通过；需网络设置生效并完成模型运行后再补验。
- M09 使用独立 agent 创作并标注的受控样本，词法结果见下方。未取得真实目标用户和主要语言的人工评估，不批准生产增强，也不宣称推荐质量或点击率在生产提高。

## 冻结语料的词法质量评价

[评价集说明](../testdata/recommendation/README.md)与 JSON/校验摘要一并提交。共有 160 篇中英日合成短帖、18 个用户画像、2,880 条完整三级判断；调参主题与验收主题互斥。11 位验收用户和 3 位无兴趣控制单独评价，没有依据实验输出调整标签或权重。

冻结 SHA-256：`3f7973468936ff2a2a873d6fc2300511113cca4b89d82ce6e5131a36ae656174`。

`TestMeiliQualityEvaluation` 使用真实 Meilisearch 1.55.0、独立 SQLite 资格、实际 provider/候选选择/排名代码；标签答案和自然语言意图不会进入索引或排序。固定候选重排与完整召回路径分别输出。词法索引版本为 `discovery-v1-rrf60-beta05-9cba6591f0b26b59`。

| 验收用户平均值 | 规则 | Meili 词法 |
| --- | ---: | ---: |
| 端到端 NDCG@20 | 0.272520 | 0.358356 |
| 兴趣候选 Recall@80 | 0.193388 | 0.226446 |
| 完整最终候选召回率 | 0.617769 | 0.650826 |
| 前 20 条不同作者数 | 16.545 | 16.818 |

预先固定的 20,000 次按用户配对 bootstrap 给出 NDCG 差值 `+0.085836`，95% 区间 `[+0.056056, +0.118728]`；中文、英文、日文兴趣标签组及有明确兴趣的新账号组均未退化。无兴趣的 3 个控制账号在各模式的排名一致、实际模式仍为 rules，其 NDCG 为 null，不以满分计入平均。互动均为零，因此“最新”和“热度”两个辅助基线重合，NDCG 均为 0.108310；这不代表真实热度信号已被评价。

结论：**词法增强在这份合成受控样本上通过预设质量门槛**。语义混合明确为 `not_run`，没有以词法结果代替。样本全是本地作者、无社交图，且用户主题相互关联；这不是独立真实人群实验、联邦实例多样性或点击率证明。生产审核值保持不变。

第二次独立执行中，18 用户 × 4 模式的 72 组结果、432 个排序/候选/来源字段全部相同，业务代码摘要及相关性指标也一致。Go 执行 0.725 秒，Python 受控词法门槛检查退出 0；摘要保存在 `/tmp/community-meili-quality-reproducibility.json`。没有以重新执行的结果修改冻结标签。

可复现命令（仓库根目录，使用隔离引擎）：

```sh
source /workspace/.community-setup/activate.sh
GOTMPDIR=/tmp GOMAXPROCS=2 DISCOVERY_TEST_MEILI_QUALITY=1 \
  DISCOVERY_TEST_MEILI_URL=http://127.0.0.1:17700 \
  DISCOVERY_TEST_QUALITY_OUTPUT=/tmp/community-meili-quality.json \
  go test -p 1 ./discovery -run '^TestMeiliQualityEvaluation$' -count=1 -v
python3 scripts/recommendation/evaluate_meili_quality.py \
  --input /tmp/community-meili-quality.json \
  --output /tmp/community-meili-quality-metrics.json \
  --require-synthetic-gate meili_lexical
```

执行参数以测试文件顶部说明为准。原始 ID 排序和机器可读指标保存在上面的 `/tmp` 路径；退出成功表示对应管道/受控门槛通过，不会自动修改生产审批。

## 真实 API 与双进程词法稳定性

`scripts/recommendation/meili_api_acceptance.py` 使用独立 MySQL 数据库 `community_discovery_qa_meili_acceptance`、Redis 17779 的 DB 4、API 18120/18121 和专用 Meili 前缀。两进程使用相同签名密钥和 Meili URL；私有登录资料留在仓库外。测试前先编译，再暂停其他编译、浏览器和索引实验，避免混入测量。

种入 24 位合成作者、99 篇帖子，其中 92 篇符合公开索引资格。5 分钟固定到达率为 4 请求/秒，最多 8 个在途请求，不使用无限排队。请求交替发往两个 API 进程：

| 指标 | 实测 |
| --- | ---: |
| 计划 / 完成请求 | 1,200 / 1,200 |
| 超过在途上限未发出的请求 | 0 |
| HTTP 错误 | 0 |
| 实际模式 | 全部 `meili_lexical` |
| P50 / P95 / P99 | 29.86 / 41.49 / 60.23 ms |
| 最大延迟 | 99.15 ms |

聚合结果在 `/tmp/community-meili-api-acceptance/stable-load-lexical.json`，本轮未保存单次请求数组。这证明该合成规模下的持续正确性，不是最大吞吐量、百万帖规模或生产延迟保证。

实际 API 故障注入还验证：真实 Meili 查询失败后，两副本显示同一失败时间及 `query_unavailable`，新请求使用 rules；恢复真实查询后错误清除并恢复词法。更换 Meili 连接 URL（同一实际服务器和数据）后，两副本均对旧游标返回 409；此项只证明连接部署策略变更，不冒充真实模型版本变更。帖子改为私有或作者撤回许可时，旧索引仍保留目标文档且代次不变，旧游标的 SQL 过滤已立即排除它们。

随后分别只启动 primary 与 peer，两个真实进程各发出 10 次持有 Redis 租约的实际检索，租约持有者不同；全程 Meili 检索最大并发为 2。证据在 `real-two-process-coordination.json`。共享 URL 的路由名不是进程身份，不能仅凭路由计数证明跨进程。上述 5 分钟运行没有采样 RSS/CPU，因此不报告资源峰值；后续资源采样代码已备好，但不能回填为本次实测。结束后已清空本隔离 QA 的质量审批，两副本均回到 rules，保留数据库与索引供后续继续；总表在 `acceptance-summary.json`。

## M08 尚未完成：真实语义模型下载

`scripts/recommendation/` 提供固定版本模型下载器、仅监听环回地址的 ONNX 服务、真实多语言 smoke 检查以及质量评价器。Python 依赖已安装，脚本语法、拒绝可变 revision 和模型哈希篡改检查已通过；这些不等于模型推理通过。

已实际获取并审核模型元数据：

- 转换模型：`Xenova/multilingual-e5-small@761b726dd34fb83930e26aab4e9ac3899aa1fa78`。
- 量化 ONNX：声明 118,308,185 字节；SHA-256 `f80102d3f2a1229f387d3c81909990d8945513e347b0eab049f7de3c6f98c193`。
- tokenizer：声明 17,082,730 字节；SHA-256 `0b44a9d7b51c3c62626640cda0e2c2f70fdacdc25bbbd68038369d14ebdf4c39`。
- 转换仓库未声明许可，明确指向的上游 `intfloat/multilingual-e5-small@614241f622f53c4eeff9890bdc4f31cfecc418b3` 声明 MIT；下载器验证并记录该来源。

元数据接口已返回 200，但文件跳转的 `us.aws.cdn.hf.co` 仍被环境代理以 CONNECT 403 拒绝。这里的大小和摘要来自固定版本元数据，**不是成功下载后的实测**。环境配置草稿已增补该确切域名及已识别的 Hugging Face 下载域名，保留原有规则；草稿保存不代表规则已发布或运行环境已放行。没有绕过代理，也没有生成假向量。

网络设置生效后，按 [运行说明](../../scripts/recommendation/README.md) 校验下载、启动服务，依次补验：真实中英日推理；Meili 文档/查询 embedding 和实际 semantic 来源；冻结语料 hybrid 质量门槛；双进程模型故障/恢复、共享预算、模型版本变更撤销旧审批/窗口及旧索引隐私撤回。**以上真实语义项目本次均未完成；生产增强质量审批仍未批准。**
