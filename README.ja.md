<p align="center">
  <img src="frontend/public/logo.svg" alt="Community" width="88">
</p>

<h1 align="center">Community</h1>

<p align="center"><b>あなたの Fediverse の居場所に、小さなサーバーをひとつ。Mastodon アプリ、Misskey 風リアクション、Matrix チャットが全部入り。</b></p>

<p align="center">
  <a href="README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <b>日本語</b> · <a href="README.ru.md">Русский</a> · <a href="README.fr.md">Français</a> · <a href="README.de.md">Deutsch</a>
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

Community は Go と Vue で作られたセルフホスト型の SNS サーバーです。タイムライン、フォロー、リプライ、投票、メディア、ダイレクトメッセージがそろっていて、Mastodon や Misskey など Fediverse 全体の人たちとつながれます。チャットルームは同じプログラムに組み込まれた Matrix ホームサーバーが提供するので、追加で動かすものはありません。

デモサーバー：**https://c.hime.at**（テスト用です。本番利用はしないでください）

## なぜ Community？

Fediverse にはすでに素晴らしいサーバーがたくさんあります。Community の違いはここです：

- **いつもの Mastodon アプリがそのまま使える。** Community は Mastodon クライアント API を実装しているので、Mastodon アプリでログインできます（iOS の Toot! で確認済み）。さらに Misskey 風の機能もあります：絵文字リアクション（Unicode とカスタム絵文字）、アバターデコレーション、そしてフル機能の Web UI。リアクションは Misskey と双方向に連合します。
- **チャットも入っています。** Matrix ホームサーバーが同じサーバーに内蔵されています。Web UI からグループチャットに参加したり、Element などの Matrix クライアント（エンドツーエンド暗号化ルームを含む）で他の Matrix サーバーの人と話したりできます。Synapse を横で動かす必要はありません。
- **小さくて運用がかんたん。** Go のバイナリひとつで API、Web アプリ、連合、Matrix をまかないます。PostgreSQL、MySQL、SQLite に対応。Redis は必須、全文検索用の Meilisearch は任意です。デモサーバーではメインプロセスのメモリ使用量は約 82 MiB です。
- **サーバーごと引っ越して、フォロワーはそのまま。** Misskey や Mastodon から乗り換えるなら、サイト全体をインポートして鍵と URL を引き継ぐので、フォロー関係が切れません。詳しくは[ゼロからやり直さずに乗り換える](#ゼロからやり直さずに乗り換える)へ。
- **セットアップはブラウザで。** 初回起動時にセットアップウィザード（7 言語）が開き、新規インストールか移行か、ドメイン、データベースを選ぶだけ。完了するとウィザードは自動でロックされます。
- **あなたの言語で。** Web UI とエラーメッセージは English、简体中文、繁體中文、日本語、Русский、Français、Deutsch に対応しています。
- **おなじみの管理ツール。** リレー、カスタム絵文字管理、ドメインブロック、コンテンツフィルター、S3 / Cloudflare R2 / MinIO ストレージ、リモート画像のメディアプロキシ、Mastodon 風の `tootctl` CLI。

## ゼロからやり直さずに乗り換える

いまの Fediverse ソフトウェアから離れられない理由は、たいていこれです。各アカウントには暗号鍵のペアと固定のアドレス（アクター URL）があり、各投稿にも固定の URL があります。リモートサーバーはその鍵で署名を検証します。別のソフトウェアに入れ替えると鍵も URL も変わり、署名が通らなくなって、フォロー関係がすべて壊れます。そのため管理者はあきらめて、みんなにゼロからやり直してもらうか、一人ずつアカウントを引っ越してもらうことになります。

Community はこの問題を解決します。**Misskey や Mastodon のサイト全体を同じドメインのままインポート**し、次のものを引き継ぎます：

- 各アカウントの**元のアクター URL と RSA 鍵ペア**（インスタンス／システムアクターとその鍵も）
- **元の投稿 URL**（Misskey の `/notes/<id>`、Mastodon の `/users/<name>/statuses/<id>`）と古いメディアリンク

だからリモートサーバーはこれまでどおりあなたを信頼し、**フォロワーはフォロワーのまま**です。誰も「アカウントの引っ越し（Move）」をする必要はありません。

**引き継がれるもの**

- アカウント、プロフィール、プロフィール項目、アイコンとヘッダー、アカウントの公開範囲設定
- パスワード（互換性のある bcrypt ハッシュ。ユーザーは元のパスワードでログインできます）
- 投稿、リプライ、リノート／ブースト、CW（注意書き）、ローカル限定投稿、添付ファイル（実ファイル、チェックサム付き）、対応している投票
- ダイレクトメッセージとその会話（受信者は非公開のまま）
- フォローと承認待ちのフォローリクエスト、ブロック、ミュート（期限付きを含む）、ドメインブロック、リスト、ブックマーク、いいね
- カスタム絵文字と絵文字リアクション
- それ以外はプライベートなアーカイブに保存され、事前チェックのレポートで何がアーカイブされたか正確にわかります

**引き継がれないもの**

- ログインセッションとアプリ（OAuth）トークン：ユーザーは再ログインが必要です
- 二要素認証とセキュリティキー：二要素認証が有効なアカウントがあると、こっそり無効にするのではなく、事前チェックで止まってお知らせします
- 過去の通知。過去の投稿を他のサーバーに再送することもありません
- Misskey 固有の一部機能（一部のチャンネル、引用、チャット機能）はアーカイブのみになる場合があります
- DNS：準備ができたらドメインの切り替えはご自身で行ってください

**安心して試せます。** エクスポーターは古いデータベースを読むだけ（短い読み取り専用トランザクションで読み、中断しても再開可能）で、一切変更しません。別の Community データベースでリハーサルして、いつでも旧サーバーに戻せます。同じインポートを再実行しても重複は作られません。

**やり方：** セットアップウィザードで「Misskey から移行」または「Mastodon から移行」を選ぶと、エクスポートとインポートをまとめて実行します。コマンドラインでは 2 段階です。`scripts/migration/export_misskey.py`（Mastodon は `export_mastodon_db.py`）でエクスポートし、そのバンドルを `community-transfer --scope site --input <バンドル> --report …` でインポートします（まず事前チェック、次に `--apply`）。既定では自インスタンスのデータ（ローカルユーザー、その投稿・ファイル・フォロー、およびそれらが参照するリモートのアカウントと投稿）だけをエクスポートし、リモートコンテンツのキャッシュ全体は含みません。すべてをエクスポートするには `--include-remote-cache` を付けます。一般ユーザーも自分の Misskey／Mastodon の個人バックアップを自分のアカウントにインポートできます。Misskey 2026.10.0 と Mastodon 4.7.3 でテスト済み。詳細、チェックリスト、制限事項は [docs/data-migration.md](docs/data-migration.md) へ。

**大規模インスタンスはダイレクトモード推奨：** `community-transfer --scope site --source-dsn postgres://… --source-base-url https://your.domain` で移行元 DB を直接読み、ストリーミングでインポートします（250 MB で最大 RSS 約 110 MiB、バンドル方式は約 1.3 GiB）。`--fetch-only` の後に短い `--final-sync` を実行できます。手順：[docs/migration.md](https://github.com/kasuganosora/community/blob/master/docs/migration.md)。

## 他のソフトウェアとの比較

ざっくりと、正直に。どのプロジェクトにも良いところがあります。自分に合うものを選んでください。

| | Community | Mastodon | Misskey | GoToSocial | Akkoma |
| --- | --- | --- | --- | --- | --- |
| 言語 | Go | Ruby + Node.js | TypeScript (Node.js) | Go | Elixir |
| 動かすプロセス | アプリ 1 つ + Redis | web、streaming、Sidekiq + Redis | アプリ + Redis | アプリ 1 つ | アプリ 1 つ |
| データベース | PostgreSQL、MySQL、SQLite | PostgreSQL | PostgreSQL | PostgreSQL、SQLite | PostgreSQL |
| Mastodon アプリ | ✅（注記参照） | ✅ ネイティブ | ❌ | ✅ | ✅ |
| 絵文字リアクション | ✅ | ❌ | ✅ | ❌ | ✅ |
| 内蔵 Web アプリ | ✅ | ✅ | ✅ | 最小限（アプリ推奨） | ✅ |
| 内蔵 Matrix チャット | ✅ | ❌ | ❌ | ❌ | ❌ |
| Web セットアップウィザード | ✅ | ❌（CLI） | ❌ | ❌ | ❌（CLI） |
| Misskey/Mastodon サイト全体を同じドメインでインポート | ✅ | ❌ | ❌ | ❌ | ❌ |

**GoToSocial の軽さが好きなら：** Community も同じくらいの軽さで、さらにフル機能の Web アプリ、Misskey 風リアクション、Matrix チャット、サイト移行が加わります。GoToSocial は Redis 不要ですが、Community は Redis が必要です。

**乗り換える前に知っておきたいこと：**

- 投稿の編集は「削除して再投稿」になります。新しい投稿になり、いいねやリプライは引き継がれません。Mastodon の編集 API は `405` を返すので、編集に依存するアプリは対応が必要です。
- リアクションは Community 独自の API 拡張です。普通の Mastodon アプリではお気に入りとして表示されるか、表示されません。
- まだ若いプロジェクトです。どのソフトウェアと何をテストしたかは [docs/federation.md](docs/federation.md) にまとめています。

## クイックスタート

Docker と Compose v2.24 以降が必要です。

```bash
git clone https://github.com/kasuganosora/community.git
cd community
docker compose up -d --build
docker compose logs community | grep -A3 "install token"
```

`http://<あなたのサーバー>:8080/setup` を開き、トークンを貼り付けてウィザードに従ってください。公開サーバーにする場合は HTTPS リバースプロキシを前に置き、最終的なドメインを使ってください。ドメインはすべてのアカウントの ID の一部になり、後から変えるべきではありません。

その他のオプション（手動の `.env`、外部データベース、S3）：[docs/setup.md](docs/setup.md) と [docs/configuration.md](docs/configuration.md)。

## 必要な環境

| 区分 | ハードウェア | 構成 | できること |
| --- | --- | --- | --- |
| **最小**（機能制限あり） | 1 vCPU、1 GB RAM | SQLite + Redis、Meilisearch なし、少人数 | すべて動きますが、全文検索なし（データベース検索で代替）、メディア処理と連合のバックフィルが遅め |
| **推奨** | 2 vCPU、2〜4 GB RAM | PostgreSQL + Redis、Meilisearch は任意 | 全機能 |
| **中規模以上** | ユーザー数と連合トラフィック次第 | | [docs/tuning.md](docs/tuning.md) を参照 |

実測値（Docker で CPU／メモリにハードリミットを設定し swap 無効、2026 年 10 月 17 日。10 ユーザー、8 並列クライアントでタイムライン閲覧、投稿、投票、メディアアップロード、いいねを 10 分間）：

- **最小構成**（スタック全体を 1 CPU / 1 GB に制限）：Community はアイドル時 78 MiB、ピーク 196 MiB、Redis は 10 MiB 未満。OOM による強制終了なし。約 97 リクエスト/秒、中央値 71 ms、95 パーセンタイル 198 ms。
- **推奨構成**（PostgreSQL + Redis + Meilisearch、2 CPU）：Community のピークは 215〜250 MiB、PostgreSQL 約 300 MiB、Redis 約 30 MiB。合計 2 GB では Meilisearch（上限 704 MiB）がメモリ不足で強制終了されましたが、Community は動き続け、検索はデータベースにフォールバックしました。4 GB ではすべて問題なく、Meilisearch のピークは 733 MiB。**Meilisearch を使うなら 4 GB を目安に**（または Meilisearch 単体に 1 GB 以上）。

デモサーバー（2026 年 10 月）では Community 約 82 MiB、Redis 141 MiB、Meilisearch 932 MiB。いちばん大きいのは Meilisearch なので、任意にしています。生データと再測定の方法：[docs/benchmarks.md](docs/benchmarks.md)。動画のサムネイルには FFmpeg が必要です（Docker イメージに同梱）。

## 互換性

- **Fediverse：** Misskey、Mastodon とテスト済み（フォロー、リプライ、DM、削除、カスタム絵文字、リアクション、メディア）。ActivityPub リレーにも対応しています。
- **Matrix：** Synapse、Element Web とテスト済み（暗号化ルームと添付ファイルを含む）。
- **Mastodon アプリ：** デモサーバーで **Toot!（iOS）** を確認済み（ログインとタイムライン、2026 年 10 月）。Community は Mastodon クライアント API を実装しているので、ほとんどの Mastodon アプリも動くはずです（上記の編集の違いにご注意ください）。
- **Matrix アプリ：** Element Web で確認済み。

テストしたバージョンと既知の制限：[docs/federation.md](docs/federation.md)。

## アップグレード

`git pull && docker compose up -d --build`。データベースのマイグレーションは起動時に実行されます。**先にデータベースと `uploads/` をバックアップしてください。** 詳しくは [docs/setup.md](docs/setup.md#upgrading-an-existing-deployment-ops)。

## ドキュメント

- [セットアップウィザードと設定ファイル](docs/setup.md)
- [インストールと設定](docs/configuration.md)
- [データ移行](docs/data-migration.md)
- [機能の詳細](docs/features.md)
- [連合と互換性](docs/federation.md)
- [管理画面](docs/admin-console.md) · [tootctl](docs/tootctl.md)
- [チューニング](docs/tuning.md) · [ベンチマーク](docs/benchmarks.md)
- [アーキテクチャとコードマップ](docs/architecture.md) · [開発とテスト](docs/development.md)
- 詳細解説：[Feed の運用](docs/feed-runtime.md)、[おすすめ](docs/recommendations.md)、[リンクプレビュー](docs/link-previews.md)、[Web グループチャット](docs/matrix-web-groups.md)、[Matrix と ActivityPub の DM](docs/matrix-activitypub-direct.md)

## コントリビュート

Issue と Pull Request は [GitHub Issues](https://github.com/kasuganosora/community/issues) で歓迎します。互換性の問題を報告するときは、クライアントやサーバーのバージョン、再現手順、個人情報を除いたログを添えてください。ローカルでのビルドとテストは [docs/development.md](docs/development.md) を参照してください。

## ライセンス

Community は [MIT ライセンス](LICENSE)で公開されています。`third_party/gomatrixserverlib` に同梱している gomatrixserverlib は引き続き Apache License 2.0 です（[LICENSE](third_party/gomatrixserverlib/LICENSE) と [NOTICE](NOTICE) を参照）。

## 謝辞

Community は多くのプロジェクトの上に成り立っています：[Mastodon](https://github.com/mastodon/mastodon)、[Misskey](https://github.com/misskey-dev/misskey)、[GoToSocial](https://github.com/superseriousbusiness/gotosocial)、[Akkoma](https://akkoma.dev/AkkomaGang/akkoma)、[Matrix](https://matrix.org) 仕様と [Synapse](https://github.com/element-hq/synapse) / [Element](https://element.io)、そして ActivityPub コミュニティ。ありがとうございます。
