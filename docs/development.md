# 开发与测试 / Development & testing

> Moved from the old README.

## 本地源码开发

已验证的开发组合为 Go **1.27.1**、Node.js **26.10.0**、npm **12.2.0**、MySQL **8.4.11** 和 Redis **7.4.11**；Go 版本声明以 [go.mod](../go.mod) 为准。视频处理需要 FFmpeg。

前端使用 TypeScript **6.0.3**；当前 `typescript-eslint` 的兼容范围尚不包含 TypeScript 7。依赖版本与完整解析结果分别记录在 `go.mod` / `go.sum` 和 `frontend/package.json` / `frontend/package-lock.json` 中。

先准备可访问的 MySQL（或 PostgreSQL，`DB_CONNECTION=postgres`，见 [db/README.md](../db/README.md)）数据库和 Redis，再复制 `.env.example`。除上述必填项外，本机运行还需要填写连接参数，例如：

```dotenv
LISTEN=127.0.0.1:8080
APP_BASE_URL=http://localhost:8080
DEV_MODE=true
DB_CONNECTION=mysql
DB_HOST=127.0.0.1
DB_PORT=3306
DB_DATABASE=community
DB_USERNAME=root
REDIS_0_HOST=127.0.0.1
REDIS_0_PORT=6379
REDIS_0_DB=0
STORAGE_TYPE=local
```

保留自己设置的 `DB_PASSWORD` 和 `ENCRYPTED_KEY`，然后在仓库根目录构建并启动：

```bash
go mod download
npm --prefix frontend ci
npm --prefix frontend run build
mkdir -p uploads
go build -o community ./cmd/community
./community
```

后端会提供 `frontend/dist` 中的网页。开发前端时，可另开终端运行：

```bash
cd frontend
npm run dev
```

Vite 默认使用 `http://localhost:3000`，将 API 请求代理到 `http://localhost:8080`；可通过 `VITE_DEV_PROXY_TARGET` 修改代理目标。

## 测试

在仓库根目录运行：

```bash
go test -p 3 ./...
npm --prefix frontend test
npm --prefix frontend run lint
npm --prefix frontend run build
```

数据库相关测试默认使用内存引擎（sqlexec / SQLite）；设置 `TEST_DATABASE_URL=postgres://…` 可改在 PostgreSQL 上运行，详见 [tests/README.md](../tests/README.md)。

如果测试环境设置了 `HTTP_PROXY`，本机测试服务器的请求可能被代理；此时可仅对测试命令清除该变量：

```bash
HTTP_PROXY= http_proxy= go test -p 3 ./...
```

本次 Feed 与管理表单改动的完整后端回归通过 87 个测试包，前端通过 73 个测试文件、342 项测试及 lint、生产构建。真实浏览器验证覆盖分页、实时重连、账号切换时的私信隔离，以及后台九类设置页的编辑、保存和移动布局。详细场景、测量条件及尚未达到的性能门槛见 [本次验收记录](tasks/feed-validation-20261011.md)。测试数量会随开发变化，以实际运行输出为准。

时间线压测使用独立数据库中的 2,000 个账号、60,000 条帖子，覆盖六种时间线、20 / 40 条分页、并发 1 / 8 / 32、冷缓存及深分页。优化包括批量缓存重建、关联与互动状态批量加载、标签及媒体查询计划、游标与列表缓存隔离。测量条件、结果和限制见[时间线性能报告](timeline-performance.md)；可使用[压测脚本](../scripts/benchmark-timelines.md)对自己的数据进行复测。

Feed 可靠分发、多实例扩展与网页虚拟列表的实现细节、阶段和验收标准见 [Feed 扩展实施计划](tasks/feed_scaling_plan.md)，部署参数见 [运行说明](feed-runtime.md)。计划中的性能门槛与验收记录中的测量结果分开列出。

首页推荐现有独立的候选、排序和稳定分页，可选择公开发现或个性化，并支持不感兴趣、隐藏作者/话题与撤销。热门话题提供滚动 24 小时 / 7 天、本地 / 本站已知联邦范围及不同作者计数。首次部署推荐默认关闭；可在后台开启，Meilisearch 增强需通过对应版本的质量审核。配置、隐私边界和资源限制见 [推荐运行说明](recommendations.md)。

Matrix 使用独立的 30 个账号、100 个房间和 50,000 条时间线事件测试同步、历史分页及消息发送，并以真实 Element / Synapse 验证双向普通和加密消息、文件下载。优化包括批量状态及撤回查询、未读计数、请求取消、设备消息确认和媒体连接复用，同时修复历史权限、私有回执和异步上传问题。测量结果和限制见 [Matrix 性能报告](matrix-performance.md)，复测方法见 [Matrix 压测脚本](../scripts/benchmark-matrix.md)。
