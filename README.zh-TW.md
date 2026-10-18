<p align="center">
  <img src="frontend/public/logo.svg" alt="Community" width="88">
</p>

<h1 align="center">Community</h1>

<p align="center"><b>為你的 Fediverse 小角落準備的一台小伺服器：Mastodon 客戶端、Misskey 式表情回應和 Matrix 聊天，全部內建。</b></p>

<p align="center">
  <a href="README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <b>繁體中文</b> · <a href="README.ja.md">日本語</a> · <a href="README.ru.md">Русский</a> · <a href="README.fr.md">Français</a> · <a href="README.de.md">Deutsch</a>
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/github/go-mod/go-version/kasuganosora/community">
  <img alt="ActivityPub" src="https://img.shields.io/badge/ActivityPub-federated-8a2be2">
  <img alt="Matrix" src="https://img.shields.io/badge/Matrix-homeserver-0dbd8b">
  <a href="https://c.hime.at"><img alt="Demo" src="https://img.shields.io/badge/demo-c.hime.at-1e90ff"></a>
</p>

<p align="center">
  <img src="docs/images/desktop-home.jpg" alt="Community on desktop" width="72%">
  <img src="docs/images/mobile-about.jpg" alt="Community on mobile" width="22%">
</p>

Community 是一個用 Go 和 Vue 寫的自託管社交伺服器。時間線、追蹤、回覆、投票、媒體、私訊都有，還能和 Mastodon、Misskey 以及整個 Fediverse 上的人互動。聊天室來自同一個程式裡內建的 Matrix Homeserver，不需要再額外部署什麼。

線上體驗：**https://c.hime.at**（測試伺服器，請不要真的拿來用）。

## 為什麼選 Community？

Fediverse 上已經有很多優秀的伺服器軟體。Community 的不同之處在於：

- **繼續用你熟悉的 Mastodon 客戶端。** Community 實現了 Mastodon 客戶端 API，可以直接用 Mastodon App 登入（已用 iOS 上的 Toot! 驗證）。同時還有 Misskey 風格的功能：表情回應（Unicode 和自訂表情）、頭像裝飾，以及完整的網頁端。表情回應可以和 Misskey 雙向互通。
- **自帶聊天。** 同一個伺服器裡內建了 Matrix Homeserver。使用者可以在網頁裡加入群聊，也可以用 Element 等 Matrix 客戶端（包括端到端加密房間），和其他 Matrix 伺服器上的人聊天。不需要在旁邊再跑一個 Synapse。
- **小巧，好維護。** 一個 Go 程式同時負責 API、網頁、聯邦和 Matrix。支援 PostgreSQL、MySQL 或 SQLite。Redis 必需；全文搜尋用的 Meilisearch 可選。展示站上主行程大約只佔 82 MiB 記憶體。
- **整站搬家，追蹤者不丟。** 從 Misskey 或 Mastodon 切換過來？Community 會匯入整個網站並保留金鑰和網址，追蹤關係不會斷。詳見[換軟體，不用從頭再來](#換軟體不用從頭再來)。
- **在瀏覽器裡完成安裝。** 首次啟動會開啟安裝精靈（7 種語言），選擇全新安裝或遷移、填寫域名和資料庫即可。安裝完成後精靈自動鎖定。
- **說你的語言。** 網頁介面和錯誤提示支援 English、簡體中文、繁體中文、日本語、Русский、Français、Deutsch。
- **常用的管理工具都在。** 中繼（Relay）、自訂表情管理、域名遮蔽、內容過濾、S3 / Cloudflare R2 / MinIO 儲存、遠端圖片的媒體代理，以及 Mastodon 風格的 `tootctl` 命令列。

## 換軟體，不用從頭再來

很多人一直沒有離開現在的 Fediverse 軟體，原因通常是這樣的：每個帳號都有一對加密金鑰和一個固定網址（Actor URL），每條貼文也有固定的網址。遠端伺服器用這些金鑰校驗簽名。一旦換了軟體，金鑰和網址都變了，簽名校驗失敗，所有追蹤關係都會斷掉。於是管理員只好放棄，讓大家從零開始，或者一個個手動遷移帳號。

Community 解決的正是這個問題。它會把**整個 Misskey 或 Mastodon 網站在同一域名下匯入**，並保留：

- 每個帳號**原來的 Actor 網址和 RSA 金鑰對**（以及例項 / 系統 Actor 和它的金鑰），
- **原來的貼文網址**（Misskey 的 `/notes/<id>`、Mastodon 的 `/users/<name>/statuses/<id>`）和舊的媒體連結，

所以遠端伺服器會繼續信任你，**追蹤你的人還在追蹤你**。誰都不需要執行「遷移帳號（Move）」。

**會搬過來的**

- 帳號、個人資料、資料欄位、頭像和封面、帳號隱私設定
- 密碼（相容的 bcrypt 雜湊，使用者用原密碼就能登入）
- 貼文、回覆、轉貼、內容警告、僅本站可見的貼文、附件（真實檔案，帶校驗）以及支援的投票
- 私訊及其會話，收件人保持私密
- 追蹤和待審批的追蹤請求、拉黑、靜音（含期限）、域名遮蔽、列表、書籤、喜歡
- 自訂表情和表情回應
- 其餘內容儲存在私有歸檔中，預檢報告會明確告訴你哪些被歸檔了

**不會搬過來的**

- 登入會話和應用（OAuth）令牌：使用者需要重新登入
- 兩步驗證和安全金鑰：如果某個帳號啟用了兩步驗證，預檢會停下來告訴你，而不是悄悄關掉它
- 歷史通知；歷史內容也不會重新傳送給其他伺服器
- 部分 Misskey 獨有的功能（某些頻道、引用和聊天功能）可能只能歸檔
- DNS：準備好之後由你自己切換域名

**可以放心演練。** 匯出工具只讀取舊資料庫（使用短小的唯讀交易，中斷後可以續傳），從不修改它。你可以先在單獨的 Community 資料庫上演練，隨時回退到舊伺服器。重複執行同一份匯入也不會產生重複資料。

**怎麼做：** 在安裝精靈中選擇「從 Misskey 遷移」或「從 Mastodon 遷移」，精靈會自動完成匯出和匯入。命令列分兩步：先用 `scripts/migration/export_misskey.py`（Mastodon 用 `export_mastodon_db.py`）匯出，再用 `community-transfer --scope site --input <遷移包> --report …` 匯入（先預檢，再加 `--apply` 執行）。預設只匯出本站自己的資料（本地使用者及其貼文、檔案、追蹤，以及它們引用到的遠端帳號和貼文），不匯出整個遠端內容快取；加 `--include-remote-cache` 可匯出全部。普通使用者也可以把自己的 Misskey / Mastodon 個人備份匯入到自己的帳號。已用 Misskey 2026.10.0 和 Mastodon 4.7.3 測試。完整說明、檢查清單和限制見 [docs/data-migration.md](docs/data-migration.md)。

**大型實例建議使用直連模式：** `community-transfer --scope site --source-dsn postgres://… --source-base-url https://你的網域` 直接讀取來源資料庫並串流匯入（250 MB 實例峰值 RSS 約 110 MiB，打包方式約 1.3 GiB），支援先 `--fetch-only` 再短暫停機 `--final-sync`。操作手冊：[docs/migration.md](https://github.com/kasuganosora/community/blob/master/docs/migration.md)。

## 和其他軟體比較

一個簡單而誠實的概覽。這裡的每個專案都有各自的長處，選適合你的就好。

| | Community | Mastodon | Misskey | GoToSocial | Akkoma |
| --- | --- | --- | --- | --- | --- |
| 程式語言 | Go | Ruby + Node.js | TypeScript (Node.js) | Go | Elixir |
| 需要執行的行程 | 1 個程式 + Redis | web、streaming、Sidekiq + Redis | 程式 + Redis | 1 個程式 | 1 個程式 |
| 資料庫 | PostgreSQL、MySQL、SQLite | PostgreSQL | PostgreSQL | PostgreSQL、SQLite | PostgreSQL |
| Mastodon 客戶端 | ✅（見下方說明） | ✅ 原生 | ❌ | ✅ | ✅ |
| 表情回應 | ✅ | ❌ | ✅ | ❌ | ✅ |
| 內建網頁端 | ✅ | ✅ | ✅ | 簡單（建議用 App） | ✅ |
| 內建 Matrix 聊天 | ✅ | ❌ | ❌ | ❌ | ❌ |
| 網頁安裝精靈 | ✅ | ❌（命令列） | ❌ | ❌ | ❌（命令列） |
| 同域名匯入整個 Misskey/Mastodon 網站 | ✅ | ❌ | ❌ | ❌ | ❌ |

**如果你喜歡 GoToSocial 的輕量：** Community 和它處在同一個量級，另外提供完整的網頁端、Misskey 式表情回應、Matrix 聊天和整站遷移。GoToSocial 不需要 Redis，Community 需要。

**切換前值得知道的：**

- 編輯貼文的方式是「刪除並重發」：會生成新貼文，原來的喜歡和回覆不會帶過去。Mastodon 的編輯 API 會回傳 `405`，依賴編輯功能的 App 需要適配。
- 表情回應使用 Community 的 API 擴充套件；普通 Mastodon App 會把它顯示為普通收藏，或者不顯示。
- 這是一個年輕的專案。具體和哪些軟體測試過什麼，見 [docs/federation.md](docs/federation.md)。

## 快速開始

需要 Docker 和 Compose v2.24 或更新版本。

```bash
git clone https://github.com/kasuganosora/community.git
cd community
docker compose up -d --build
docker compose logs community | grep -A3 "install token"
```

然後開啟 `http://<你的伺服器>:8080/setup`，貼上 token，按精靈操作即可。公開部署時請在前面加一個 HTTPS 反向代理，並使用最終域名：它會成為每個帳號身份的一部分，之後不應更改。

更多選項（手動編寫 `.env`、外部資料庫、S3）：[docs/setup.md](docs/setup.md) 和 [docs/configuration.md](docs/configuration.md)。

## 執行需求

| 檔位 | 硬體 | 配置 | 效果 |
| --- | --- | --- | --- |
| **最低**（功能縮減） | 1 vCPU、1 GB 記憶體 | SQLite + Redis，不用 Meilisearch，少量使用者 | 功能都能用，但沒有全文搜尋（退回資料庫搜尋），媒體處理和聯邦回填較慢 |
| **推薦** | 2 vCPU、2–4 GB 記憶體 | PostgreSQL + Redis，Meilisearch 可選 | 全部功能 |
| **中型及以上** | 取決於使用者數和聯邦流量 | | 見 [docs/tuning.md](docs/tuning.md) |

實測結果（Docker 中設定硬性 CPU / 記憶體限制並禁用 swap，2026 年 10 月 17 日；10 個使用者、8 個併發客戶端持續 10 分鐘瀏覽時間線、發文、發起投票、上傳媒體和點贊）：

- **最低檔**（整套服務限制在 1 CPU / 1 GB）：Community 空閒 78 MiB，峰值 196 MiB，Redis 不到 10 MiB。沒有發生記憶體不足被殺。約 97 請求/秒，中位數 71 ms，95 分位 198 ms。
- **推薦檔**（PostgreSQL + Redis + Meilisearch，2 CPU）：Community 峰值 215–250 MiB，PostgreSQL 約 300 MiB，Redis 約 30 MiB。總記憶體 2 GB 時，Meilisearch（限制 704 MiB）因記憶體不足被終止，Community 照常運作，搜尋退回資料庫；4 GB 時全部正常，Meilisearch 峰值 733 MiB。**如果要用 Meilisearch，請以 4 GB 規劃**（或單獨給它至少 1 GB）。

展示站（2026 年 10 月）上 Community 約 82 MiB、Redis 141 MiB、Meilisearch 932 MiB。Meilisearch 是最佔記憶體的部分，所以它是可選的。原始資料和複測方法：[docs/benchmarks.md](docs/benchmarks.md)。影片縮圖需要 FFmpeg（Docker 映像檔已包含）。

## 相容性

- **Fediverse：** 已和 Misskey、Mastodon 測試（追蹤、回覆、私訊、刪除、自訂表情、表情回應、媒體）。也支援 ActivityPub 中繼。
- **Matrix：** 已和 Synapse、Element Web 測試，包括加密房間和附件。
- **Mastodon App：** 已在展示站用 **Toot!（iOS）** 驗證（登入和時間線，2026 年 10 月）。Community 實現了 Mastodon 客戶端 API，所以大多數 Mastodon App 應該也能使用（注意上面提到的編輯差異）。
- **Matrix App：** 已用 Element Web 驗證。

測試版本和已知限制：[docs/federation.md](docs/federation.md)。

## 升級

`git pull && docker compose up -d --build`。資料庫遷移會在啟動時自動執行。**請先備份資料庫和 `uploads/`。** 詳見 [docs/setup.md](docs/setup.md#upgrading-an-existing-deployment-ops)。

## 文件

- [安裝精靈與配置檔案](docs/setup.md)
- [安裝與配置](docs/configuration.md)
- [資料遷移](docs/data-migration.md)
- [功能詳解](docs/features.md)
- [聯邦與相容性](docs/federation.md)
- [管理後臺](docs/admin-console.md) · [tootctl](docs/tootctl.md)
- [調優](docs/tuning.md) · [效能測試](docs/benchmarks.md)
- [架構與程式碼導航](docs/architecture.md) · [開發與測試](docs/development.md)
- 深入閱讀：[Feed 執行說明](docs/feed-runtime.md)、[推薦](docs/recommendations.md)、[連結預覽](docs/link-previews.md)、[網頁群聊](docs/matrix-web-groups.md)、[Matrix 與 ActivityPub 私訊](docs/matrix-activitypub-direct.md)

## 參與貢獻

歡迎在 [GitHub Issues](https://github.com/kasuganosora/community/issues) 提交問題和 Pull Request。報告相容性問題時，請附上客戶端或伺服器版本、復現步驟和脫敏日誌。本地構建和執行測試見 [docs/development.md](docs/development.md)。

## 授權條款

Community 以 [MIT 授權條款](LICENSE)發布。`third_party/gomatrixserverlib` 中內附的 gomatrixserverlib 副本仍採用 Apache License 2.0（見其 [LICENSE](third_party/gomatrixserverlib/LICENSE) 與 [NOTICE](NOTICE)）。

## 致謝

Community 站在許多專案的肩膀上：[Mastodon](https://github.com/mastodon/mastodon)、[Misskey](https://github.com/misskey-dev/misskey)、[GoToSocial](https://github.com/superseriousbusiness/gotosocial)、[Akkoma](https://akkoma.dev/AkkomaGang/akkoma)、[Matrix](https://matrix.org) 規範以及 [Synapse](https://github.com/element-hq/synapse) / [Element](https://element.io)，還有整個 ActivityPub 社群。謝謝你們。
