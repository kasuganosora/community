# 推荐系统研究：资料、证据与取舍

日期：2026-10-14（Asia/Shanghai）。研究开始时 Community 为 `f63fca0`；业务代码盘点基线仍为 `379b973`，其后的推荐相关提交仅修改文档。本文配合[实施方案](community-recommendation-research.md)，记录实际阅读的论文、官方文档和固定版本源码，不是推荐功能的完成报告。

## 1. 最符合本项目的组合

Community 是本站可见数据有限、公开内容更新快的联邦社区，已有 Go、SQL、Redis 和可选 Meilisearch，没有可靠的逐帖曝光历史。首先要让用户找到相关的新帖，同时保持发现偏好、当前权限、可解释性和有界成本。

| 资料 | 为什么相关 | 本轮采用 | 暂不采用或限制 |
| --- | --- | --- | --- |
| BPR 论文 | 解释隐式互动与缺失数据的区别 | 区分未观察、正互动、明确拒绝；撤销点赞不生成负反馈 | 不立即训练模型，不把缺失行为当“不喜欢” |
| 推荐抽样评估论文 | 避免只在小候选集取得好分数就宣布成功 | 召回、固定候选重排、端到端分别评估 | 不将 sampled-NDCG 当全候选指标；首版不实现复杂估计器 |
| Meilisearch 官方文档与 SDK | 可以直接利用已有检索服务 | 按兴趣选共享桶，多路词法召回，Go 内融合；能力和索引就绪检测 | 引擎在线不等于语义模型可用；不增加首屏模型调用 |
| Mastodon 源码 | 同为联邦公开内容，话题热榜和治理最贴近场景 | 独立参与者、最小支持量、时间衰减、独立禁推治理 | 日桶/HLL/保留峰值不能直接满足滚动窗口和撤回统计 |
| Bluesky feed-generator 源码 | 排名与内容加载的接口边界简单 | 内部 ID 骨架，批量授权与序列化，短期候选 | starter 不是完整系统；不复制其认证和不完整时间游标 |
| ranx 的 RRF 实现 | 验证无需统一原始评分尺度的融合机制 | 按名次融合，明确缺席与去重，加入来源家族权重约束 | `k=60` 只是起点；未阅读 RRF 原论文，未验证最优值 |
| LightGCN 论文、Gorse 源码 | 为后续学习排序和成熟引擎作对照 | 保留反馈分类和后续对照路线 | 现阶段不增加图训练与整套推荐引擎的运行生命周期 |

结论是**沿用现有基础设施，先做好候选选择与轻量重排**。不是先选一个复杂模型再为它搜集数据。所有上游参数和论文结果都不能当作 Community 的质量或性能结果。

## 2. 论文原文：反馈、评估和模型取舍

### P1. BPR: Bayesian Personalized Ranking from Implicit Feedback

Steffen Rendle、Christoph Freudenthaler、Zeno Gantner、Lars Schmidt-Thieme；UAI 2009，pp.452–461。实际阅读[固定版本 PDF 镜像](https://github.com/hongleizhang/RSPapers/blob/9f7ee11065cd850f5c4814c1dcc4720c91511ce9/02-General_RS/2009-BPR%20Bayesian%20Personalized%20Ranking%20from%20Implicit%20Feedback.pdf)。

- p.453 §3：未观察的用户—物品对混合了真正负反馈与缺失值，无法仅凭“没有互动”区分。
- p.454 §3.2：将已观察项目相对未观察项目视为更偏好，是模型的成对排序假设，不是给每个未观察项目贴上“不喜欢”的事实标签。
- pp.454–455 §4/4.1：目标是用户特定的两两排序优化，不是把缺失值全部设为 0 的分类。

**方案改变：**明确兴趣和明确拒绝分开；API 返回不等于曝光，曝光不等于喜欢或拒绝；取消互动只撤回正信号。后续若训练，先比较简单 MF/BPR 基线，另行定义采样分布和曝光偏差。论文不解决联邦隐私或权限，不能代替资格层，也没有证明 BPR 在本站胜过规则。

### P2. Towards Reliable Item Sampling for Recommendation Evaluation

Dong Li、Ruoming Jin、Zhenming Liu、Bin Ren、Jing Gao、Zhi Liu；AAAI 2023。实际阅读的是 **2022-11-28 arXiv v1 预印本**的[固定 PDF 镜像](https://github.com/hongleizhang/RSPapers/blob/9f7ee11065cd850f5c4814c1dcc4720c91511ce9/00-Latest_Papers/AAAI2023/%5B22.11%5D%20Towards%20Reliable%20Item%20Sampling%20for%20Recommendation%20Evaluation.pdf)，不是声称读取了出版终稿。[arXiv 原始定位](https://arxiv.org/abs/2211.15743)在本环境未成功访问。

- PDF p.2 §2.1：抽样集合里的排名与全物品集合里的排名是不同量，sampled top-K 与 global top-K 对应不同指标。
- §3/§4、§6：提出估计和自适应采样方法；普通小样本评估在小 K 区域可能没有足够分辨率。结论不是“所有采样都无效”。

**方案改变：**分别报告候选召回覆盖、同一固定候选集合上的 NDCG@20，以及完整链路的最终列表。人工相关性采用跨来源盲评，说明标注覆盖，未标注不默认为无关。必须采样时说明分布、数量、校正和方差。当前规模受控的基准不必先实现论文中的复杂估计器。

冻结关系、热度、首次收录时间等全部特征，防止未来数据进入历史回放，是本项目另外提出的评估约束，不把它归为这篇论文的专门结论。这篇论文也不证明点击是因果收益。

### P3. LightGCN: Simplifying and Powering Graph Convolution Network for Recommendation

Xiangnan He、Kuan Deng、Xiang Wang、Yan Li、Yongdong Zhang、Meng Wang；SIGIR 2020，DOI `10.1145/3397271.3401063`。实际阅读[作者本人站点仓库的 PDF](https://github.com/hexiangnan/hexiangnan.github.io/blob/0cbb3e882eb2a87c0c2394c00a4157ed84a1c2ef/papers/sigir20-LightGCN.pdf)。

- §3.1.1–3.1.3：在用户—物品图上做邻居 embedding 线性传播与层组合，去掉特征变换和非线性激活。
- §3.3：仍训练初始层 embedding，使用 BPR 成对损失；“Light”不等于没有训练。
- §4/Table 2：实验数据是 Gowalla、Yelp、Amazon-Book，不是本站这种不完整联邦互动图。

**取舍：**新帖/新作者缺少图边、本站观察稀疏，而标签与正文可立即用于冷启动，因此延后图模型。这是场景适配推断，不是论文证明本站图模型必然失败。后续若数据和实验支持，可增加离线图召回源，仍不得绕过资格检查。论文链接的作者实现没有在本次逐文件审计。

## 3. Meilisearch：真实能力与接入边界

以下均来自实际阅读的[官方文档仓库固定版本](https://github.com/meilisearch/documentation/tree/0d96f60225eabdfed0721ab95e39b3605a764bed)，提交时间为 2026-10-12T23:03:35Z。官网在当前网络不可用，引用的是官方文档源文件，不把它等同于本站部署版本已具备所有能力。

| 证据 | 核查结果 | 方案修正 |
| --- | --- | --- |
| [ranking_rules.mdx](https://github.com/meilisearch/documentation/blob/0d96f60225eabdfed0721ab95e39b3605a764bed/capabilities/full_text_search/relevancy/ranking_rules.mdx#L71) | 顺序 bucket 排名，后级规则只能打破前级平局 | 当前 `created_at:desc` 不是纯时间排序；不称其为 BM25 |
| [boost_results_across_indexes.mdx](https://github.com/meilisearch/documentation/blob/0d96f60225eabdfed0721ab95e39b3605a764bed/capabilities/multi_search/how_to/boost_results_across_indexes.mdx#L11) | federation weight 乘的是相关性分数，不是倒数排名融合 | 普通 multi-search 保留每路结果，由 Go 执行 RRF |
| [multi_search.mdx](https://github.com/meilisearch/documentation/blob/0d96f60225eabdfed0721ab95e39b3605a764bed/capabilities/multi_search/getting_started/multi_search.mdx) / [OpenAPI](https://github.com/meilisearch/documentation/blob/0d96f60225eabdfed0721ab95e39b3605a764bed/assets/release-assets/meilisearch-openapi.json) | 普通模式分路返回；任一路查询失败可能导致整批没有部分结果 | 可选语义路隔离失败或保留上一完整代，不假设会拿到部分词法结果 |
| [custom_hybrid_ranking.mdx](https://github.com/meilisearch/documentation/blob/0d96f60225eabdfed0721ab95e39b3605a764bed/capabilities/hybrid_search/advanced/custom_hybrid_ranking.mdx#L6) | `hybrid.semanticRatio` 为 0–1，默认 0.5，依赖 embedder | 不把比例当成语义结果条数占比；显式配置且就绪才增强 |
| [async_operations.mdx](https://github.com/meilisearch/documentation/blob/0d96f60225eabdfed0721ab95e39b3605a764bed/capabilities/indexing/tasks_and_batches/async_operations.mdx#L8) / 上述 OpenAPI 的 swap-indexes | 写入、设置、删除等异步；swap 原子，但队列任务不随之改写 | 检查目标 task 成功；采用独立 generation 和 ready 指针，避免误以为 swap 与 Redis 一起原子提交 |
| [tokenization.mdx](https://github.com/meilisearch/documentation/blob/0d96f60225eabdfed0721ab95e39b3605a764bed/capabilities/indexing/advanced/tokenization.mdx#L15) / [typo_tolerance_settings.mdx](https://github.com/meilisearch/documentation/blob/0d96f60225eabdfed0721ab95e39b3605a764bed/capabilities/full_text_search/relevancy/typo_tolerance_settings.mdx#L24) | 中文使用专门分词；默认 5/9 字符才给 1/2 个错字预算 | 专测两字中文和中英混合；标签用规范化字段精确过滤，不能依赖 `#标签` 全文查询 |
| [personalized_search.mdx](https://github.com/meilisearch/documentation/blob/0d96f60225eabdfed0721ab95e39b3605a764bed/capabilities/personalization/getting_started/personalized_search.mdx#L79) | 空查询下 userContext 只能重排固定集合，不能改变召回子集 | 按用户兴趣先选桶，不能所有用户只拿同一份热门再换顺序；P1 不启用此模型重排 |
| [recommendations.mdx](https://github.com/meilisearch/documentation/blob/0d96f60225eabdfed0721ab95e39b3605a764bed/capabilities/personalization/getting_started/recommendations.mdx) | `/similar` 是依赖 embedder 的物品向量近邻 | 后续可作为后台公开帖近邻池，不等于完整个性化推荐系统 |

另外实际核查当前 Go SDK **v0.36.3** 的 [types.go](https://github.com/meilisearch/meilisearch-go/blob/3698ba9e0359ec5fb44cb2f9ec319c034478fee3/types.go#L670)：`SemanticRatio float64` 带 `omitempty`，非 nil Hybrid 的零值可能不进入 JSON，不能保证服务端收到纯词法参数。纯词法使用 `Hybrid=nil`，未来实施需增加 wire JSON 契约检查。

推荐索引独立于可能包含其他可见性内容的通用搜索索引。就绪信息必须绑定 schema、实际服务器能力、完整回填、异步任务成功、数据滞后；模型模式另绑定模板、维度和模型版本。首屏只读已建好的共享池，不等待外部模型或检索请求。最终 SQL 当前资格始终有效，不能拿“索引已经过滤过”替代。

## 4. 开源代码：采用设计，也检查示例的缺口

### C1. Mastodon

固定版本 `7f5bc79930136abe1a4aeb0f4530be1b08f9dbbb`，AGPLv3。以下只参考设计与行为，不移植其代码。

- [tags.rb](https://github.com/mastodon/mastodon/blob/7f5bc79930136abe1a4aeb0f4530be1b08f9dbbb/app/models/trends/tags.rb#L94)：以昨日独立账号数为 expected，当日人数为 observed；上涨并达到门槛后计算 `(observed-expected)^2/expected`，保留峰值再衰减。默认支持量 5、峰值半衰期 4 小时等是上游参数，不是显著性检验，也不是本站最佳值。
- [history.rb](https://github.com/mastodon/mastodon/blob/7f5bc79930136abe1a4aeb0f4530be1b08f9dbbb/app/models/trends/history.rb#L13)：UTC 日桶，uses 为普通计数，accounts 为 HLL；跨日 PFCOUNT 合并集合，没有把各日去重人数相加。HLL 不能撤销某个作者贡献；每日批次处理也不等于总工作量有界。
- [statuses.rb](https://github.com/mastodon/mastodon/blob/7f5bc79930136abe1a4aeb0f4530be1b08f9dbbb/app/models/trends/statuses.rb#L91)：帖子趋势检查 public、discoverable 等资格，排除回复/CW/敏感内容。标签入口没有完全相同的 discoverable 检查，不能说上游所有榜单已经共用一个资格层。Community 保留自己的 CW/warn 策略。
- [tag_trend.rb](https://github.com/mastodon/mastodon/blob/7f5bc79930136abe1a4aeb0f4530be1b08f9dbbb/app/models/tag_trend.rb#L14)：允许推广与热度排名是独立条件。Community 增加站级作者/帖子/话题禁推，独立于删除/封禁。
- [follow_recommendation.rb](https://github.com/mastodon/mastodon/blob/7f5bc79930136abe1a4aeb0f4530be1b08f9dbbb/app/models/follow_recommendation.rb#L30)：关注路采用近 30 天活跃本站用户的关注量；互动路聚合近期帖子互动量，没有同样限定互动者活跃。两者经 `x/(1+x)` 饱和，不直接相信远端自报粉丝总量。Community 另加有效互动者去重等约束，不能说上游已经实现。该 SQL 面向 PostgreSQL，不能复制到本项目三方言实现。

**方案改变：**热榜分别表达滚动窗口规模、相邻等长窗口势头、新鲜度；有最小支持量、平滑与贡献上限，覆盖中断不显示虚假增长。精确计数仍由当前 SQL 计算，标签删除/禁推后不能继续靠旧峰值霸榜。HLL 只作为未来有明确误差口径的候选初筛选项。本站观察到的参与者也不等于全网真实人数。

### C2. Bluesky feed-generator

固定版本 `70e172e16c659167707a7ef65eef7e37fdb606ff`，MIT。

- [README](https://github.com/bluesky-social/feed-generator/blob/70e172e16c659167707a7ef65eef7e37fdb606ff/README.md#L64)：返回有序 post URI 与可选理由，随后加载内容；仓库自述是未完备的 starter。仅借职责边界，不把其部署拓扑说明当现网权威。
- [README 游标说明](https://github.com/bluesky-social/feed-generator/blob/70e172e16c659167707a7ef65eef7e37fdb606ff/README.md#L122)建议时间与 CID 共同标识，然而 [whats-alf.ts](https://github.com/bluesky-social/feed-generator/blob/70e172e16c659167707a7ef65eef7e37fdb606ff/src/algos/whats-alf.ts#L7) 排序使用时间/CID、实际翻页仅使用时间 `<`：同时间条目超过一页可能漏项。这是源码推断，未运行上游复现。
- [subscription.ts](https://github.com/bluesky-social/feed-generator/blob/70e172e16c659167707a7ef65eef7e37fdb606ff/src/subscription.ts#L20)有候选删除路径，但 `indexedAt=now()` 不能作为本站远端旧帖的新鲜度。[feed-generation.ts](https://github.com/bluesky-social/feed-generator/blob/70e172e16c659167707a7ef65eef7e37fdb606ff/src/methods/feed-generation.ts#L22)的认证只是注释示例。

**方案改变：**在同一个 Go 程序内分开候选骨架、排名、授权和序列化；保持自己的绑定用户/偏好的签名快照游标，同分以稳定 ID 兜底。只借内部接口，不引入 ATProto 或假设有全网 firehose。

### C3. Gorse

固定版本 `3d664dff4bc2af4b796da63b4d69d6d83b1406dc`，Apache-2.0。实际阅读 [README](https://github.com/gorse-io/gorse/blob/3d664dff4bc2af4b796da63b4d69d6d83b1406dc/README.md#L64) 和 [config.go](https://github.com/gorse-io/gorse/blob/3d664dff4bc2af4b796da63b4d69d6d83b1406dc/config/config.go#L275)。

README 区分 master 的训练/配置、worker 的离线推荐和 server 的在线接口；配置区分 positive、negative、read 反馈并有优先级/TTL。可借其反馈分类，但当前项目没有证明需要完整训练与运行链路，因此首版不部署。Gorse 有单机试用方式，延后不意味着它无法小规模部署，而是我们优先复用已有系统，避免新增数据同步与训练生命周期。

### C4. ranx 的 RRF 实现

固定版本 `7363db0c35e92e90d6fa6fe73907b760678f765e`，MIT。实际阅读 [rrf.py](https://github.com/AmenRa/ranx/blob/7363db0c35e92e90d6fa6fe73907b760678f765e/ranx/fusion/rrf.py)：逐路按 `1/(k+rank)` 计分再合并，rank 从 1 起，默认 `k=60`。

代码能核查公式和默认值，不能证明其在本站最优。原 RRF 论文（Cormack、Clarke、Büttcher，SIGIR 2009，DOI `10.1145/1571941.1572114`）本次未取得原文，不编造页码或实验结论。项目增加用户权重、来源家族预算、每路去重和固定上界归一化，是工程扩展；并非该库已经替我们实现或验证。

同样未读取 MMR 原论文；方案中的可选 `relevance - λ × redundancy` 是基于现有标签/链接/作者的有界贪心试验，以 `λ=0` 为对照，默认不启用，不声称论文复现或语义去重。

## 5. 对实施与验收的直接影响

| 方案位置 | 修正点 | 应当证明什么 |
| --- | --- | --- |
| §3、§4.3、§8 | 当前资格 + 个人反馈 + 站级禁推 | 高分和缓存不能绕过禁推；个人 API 不能修改站级记录 |
| §4.1、§4.6 | 先按兴趣选桶，保留召回来源；长尾缺覆盖有明确回退 | 不同兴趣得到相关子集，不只是同一热门榜换顺序；理由有真实证据 |
| §4.2、§4.6 | 固定特征尺度、RRF 来源家族预算 | 重复查询不刷票；缺席贡献为零；算法版本和参数可复现 |
| §4.4 | 骨架与最终加载分离、稳定快照 | 同分/同时间不漏不重，过滤后有限补页；不能借任意 ID 越权 |
| §4.6 | 索引任务/能力/代次就绪及 SDK 参数边界 | 回填中不启用、失败可降级；词法不意外触发混合查询 |
| §4.7 | 规模与势头分离、可撤回精确统计 | 标签从 30 名合格作者降到 2 名时失去热门资格；收录恢复不伪造爆发 |
| §5、§7 | 未观察不当负例，召回和重排分开验收 | 不靠小负采样或未来信息取得虚高分数；没有曝光数据不报 CTR 收益 |

## 6. 阅读和验证边界

已实际读取 3 篇 PDF 原文（两篇公开镜像、一篇作者站点源文件），官方 Meilisearch 文档源/SDK，以及上述固定版本开源文件。外部官网和部分论文站点在当前环境不可用，未把摘要或无法访问的网页当作已读证据。

本轮没有运行上游推荐系统、部署新模型、训练参数或测量 Community 推荐质量/吞吐；未把论文结果当本站实测。原文和下载源码留在临时研究目录，不加入仓库，不复制第三方实现或新增运行依赖。主方案中的权重、容量、门槛和延迟仍是待验证的初始预算，实施前以实际数据库、Meilisearch 版本和资源重新核验。
