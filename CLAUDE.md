# Community Matrix Homeserver - Claude Code 开发指南

## Complement 测试方法

### 关键文件路径

- **Complement 入口**: `cmd/complement/main.go` (精简版服务器，无 i18n 依赖)
- **社区完整入口**: `cmd/community/main.go` (完整服务器，有 i18n 依赖，不用于 Complement)
- **Dockerfile**: `Dockerfile.complement` (项目根目录，使用预编译二进制)
- **二进制输出**: `build/complement-linux` (Linux amd64，注意文件名是 **complement-linux** 不是 community-linux)
- **WSL 中的 Complement 框架**: `~/complement/` (从 github.com/matrix-org/complement 克隆)

### 完整测试步骤

#### 1. 编译 Linux 二进制（在 Windows/Cygwin 中）

```bash
cd /d/code/community
GOOS=linux GOARCH=amd64 go build -o build/complement-linux ./cmd/complement/
```

**注意**: 必须用 `./cmd/complement/` 不是 `./cmd/community/`，后者依赖 i18n 文件在容器中不存在会 panic。

#### 2. 构建 Docker 镜像（通过 WSL）

```bash
cd /d/code/community
wsl -e docker build --no-cache -t complement-community:latest -f Dockerfile.complement .
```

`--no-cache` 确保使用最新的二进制。如果确认二进制已更新，可以省略 `--no-cache` 加快构建。

#### 3. 运行测试（通过 WSL）

WSL 中的 Go 路径需要手动设置，且需要用 `sg docker` 获取 Docker 权限：

```bash
# 运行全部 csapi 测试
wsl -e bash -lc 'export PATH=/usr/local/go/bin:/usr/bin:/bin:/usr/sbin:/sbin && cd ~/complement && sg docker -c "COMPLEMENT_BASE_IMAGE=complement-community:latest go test -v -count=1 -timeout 600s ./tests/csapi/..." 2>&1 | grep -E "^--- (PASS|FAIL): Test" | sort'

# 运行特定测试（例如 TestServerNotices）
wsl -e bash -lc 'export PATH=/usr/local/go/bin:/usr/bin:/bin:/usr/sbin:/sbin && cd ~/complement && sg docker -c "COMPLEMENT_BASE_IMAGE=complement-community:latest go test -run \"TestServerNotices\" -v -count=1 -timeout 120s ./tests/csapi/..."'

# 带服务器日志的调试模式
wsl -e bash -lc 'export PATH=/usr/local/go/bin:/usr/bin:/bin:/usr/sbin:/sbin && cd ~/complement && sg docker -c "COMPLEMENT_BASE_IMAGE=complement-community:latest COMPLEMENT_ALWAYS_PRINT_SERVER_LOGS=1 go test -run \"TestServerNotices\" -v -count=1 -timeout 120s ./tests/csapi/..."'
```

**注意**: 不要直接用 `wsl -e bash -c 'export PATH=...'`，因为 Windows PATH 中的括号 `(` 会导致 bash 语法错误。必须用 `-lc` 并在命令开头重置 PATH。

### 常见陷阱

1. **二进制文件名**: `Dockerfile.complement` 里 COPY 的是 `build/complement-linux`，不是 `build/community-linux`。构建时务必用 `-o build/complement-linux`。
2. **入口文件**: 用 `cmd/complement/main.go`，不要用 `cmd/community/main.go`（后者需要 i18n 文件）。
3. **Docker 缓存**: 如果改了代码但测试结果不变，检查是否用了 `--no-cache`，以及二进制时间戳是否更新。
4. **WSL PATH 污染**: Windows PATH 含 `Program Files (x86)` 等带括号路径，传入 WSL 会破坏 bash。必须在 WSL 命令中重置 PATH。
5. **complement.exe**: 项目根目录的 `complement.exe` 是 `cmd/complement/main.go` 编译的 **Windows 服务器二进制**，不是测试运行器。Complement 测试框架在 WSL 的 `~/complement/` 目录中通过 `go test` 运行。

### 一键测试脚本

编译 + 构建 + 运行全部测试：

```bash
cd /d/code/community && \
GOOS=linux GOARCH=amd64 go build -o build/complement-linux ./cmd/complement/ && \
wsl -e docker build --no-cache -t complement-community:latest -f Dockerfile.complement . 2>&1 | tail -3 && \
wsl -e bash -lc 'export PATH=/usr/local/go/bin:/usr/bin:/bin:/usr/sbin:/sbin && cd ~/complement && sg docker -c "COMPLEMENT_BASE_IMAGE=complement-community:latest go test -v -count=1 -timeout 600s ./tests/csapi/..." 2>&1 | grep -E "^--- (PASS|FAIL): Test" | sort'
```

### 当前测试状态 (2026-02-23)

- **PASS**: 95 个顶级测试
- **FAIL**: 5 个顶级测试
- 剩余失败测试（详见 `docs/complement-remaining-failures.md`）:
  - TestSyncTimelineGap: 联邦事件 backfill 后 sync limited 标志和事件排序问题
  - TestDeviceListUpdates: 远程用户 leave 事件未出现在 /sync 中
  - TestMessagesOverFederation: 联邦 backfill 只取一批(~100)，需要迭代取完整历史
  - TestPushRuleRoomUpgrade: createRoom 不支持 creation_content.predecessor
  - TestSync: 联邦 redaction 子测试，同 TestSyncTimelineGap 根因

## Matrix Homeserver 代码结构

- `matrix/homeserver/` - 所有 Matrix 协议实现
  - `handlers.go` - HTTP 路由处理（Client-Server API）
  - `admin.go` - Synapse 兼容管理 API（含 AdminAuthMiddleware）
  - `room.go` - 房间创建/加入/离开/邀请
  - `sync.go` - /sync 端点实现
  - `search.go` - 搜索 API
  - `media.go` - 媒体上传/下载
  - `federation.go` - 联邦 API
  - `models.go` - 数据库模型
  - `relations.go` - 包含 MatrixAuthMiddleware
