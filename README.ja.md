# isola - Git Worktree Server Manager

[![CI](https://github.com/cyucelen/isola/actions/workflows/ci.yaml/badge.svg)](https://github.com/cyucelen/isola/actions/workflows/ci.yaml)
[![codecov](https://codecov.io/gh/cyucelen/isola/branch/main/graph/badge.svg)](https://codecov.io/gh/cyucelen/isola)
[![Go Report Card](https://goreportcard.com/badge/github.com/cyucelen/isola)](https://goreportcard.com/report/github.com/cyucelen/isola)
[![Go Reference](https://pkg.go.dev/badge/github.com/cyucelen/isola.svg)](https://pkg.go.dev/github.com/cyucelen/isola)
![Go Version](https://img.shields.io/github/go-mod/go-version/cyucelen/isola)

**isola** は [git worktree](https://git-scm.com/docs/git-worktree) ごとに複数の dev server を自動管理する CLI ツールです。ポートの自動割り当て、環境変数の自動注入、`*.localhost` サブドメインルーティングによるリバースプロキシを提供します。

> English version: [README.md](./README.md)

---

## デモ

![isola workflow demo](./demo/demo-workflow.gif)

---

## 特徴

- **マルチサービス** — フロントエンド、バックエンド、任意の数のサービスを worktree ごとに定義
- **ポート自動割り当て** — ハッシュベース (FNV32) のポート割り当て。worktree 間のポート衝突なし
- **サブドメインリバースプロキシ** — `branch-name.localhost:<port>` で任意の worktree にアクセス (`/etc/hosts` の編集不要)
- **HTTPS プロキシ** — 自動生成証明書またはカスタム証明書による HTTPS 対応。Secure Cookie や Service Worker が必要なローカル開発に
- **環境変数の自動注入** — `$PORT`、`$ISOLA_BRANCH`、`$ISOLA_BACKEND_URL` 等を自動設定
- **TUI ダッシュボード** — ターミナル上のインタラクティブ UI でサービスの起動・停止・監視
- **プロセスライフサイクル管理** — グレースフルシャットダウン (SIGTERM → SIGKILL)、ログファイル、古い PID の自動クリーンアップ
- **worktree ごとのオーバーライド** — ブランチ別にコマンド、ポート、環境変数をカスタマイズ
- **AI エージェント対応** — `isola ls --json` で URL 情報を含む JSON 出力。エンドポイントの自動発見に対応

---

## クイックスタート

### 1. インストール

![Install demo](./demo/demo-install.gif)

```bash
# Homebrew
brew install cyucelen/tap/isola

# Go install
go install github.com/cyucelen/isola@latest

# またはソースからビルド
git clone https://github.com/cyucelen/isola.git
cd isola
make build
```

### 2. 初期化

![Init demo](./demo/demo-init.gif)

```bash
cd your-project
isola init
# リポジトリルートに .isola.toml を作成
```

### 3. 設定

`.isola.toml` をプロジェクトに合わせて編集:

```toml
[services.frontend]
command = "pnpm run dev"
dir = "frontend"
port_range = { min = 3100, max = 3199 }
proxy_port = 3000

[services.backend]
command = "source .venv/bin/activate && python manage.py runserver 0.0.0.0:$PORT"
dir = "backend"
port_range = { min = 8100, max = 8199 }
proxy_port = 8000

[env]
NODE_ENV = "development"
```

### 4. サービス起動

```bash
isola up            # 現在の worktree の全サービスを起動
isola up --all      # 全 worktree の全サービスを起動
```

### 5. プロキシ起動

```bash
isola proxy start
# :3000 → frontend サービス
# :8000 → backend サービス

# HTTPS で起動する場合
isola proxy start --https
# 自動生成された証明書で HTTPS プロキシを起動
```

### 6. ブラウザで開く

```bash
isola open                    # http://main.localhost:3000 を開く
isola open --service backend  # http://main.localhost:8000 を開く
```

---

## コマンド一覧

| コマンド                        | 説明                                             |
| ------------------------------- | ------------------------------------------------ |
| `isola init`                  | `.isola.toml` 設定ファイルを作成               |
| `isola up`                    | 現在の worktree のサービスを起動                 |
| `isola up --all`              | 全 worktree のサービスを起動                     |
| `isola up --service`          | 特定のサービスのみ起動                           |
| `isola down`                  | 現在の worktree のサービスを停止                 |
| `isola down --all`            | 全 worktree のサービスを停止                     |
| `isola ls`                    | 全 worktree のサービス、ポート、状態、PID を表示 |
| `isola dash`                  | インタラクティブ TUI ダッシュボードを起動        |
| `isola proxy start`           | リバースプロキシを起動 (フォアグラウンド)        |
| `isola proxy start --https`   | HTTPS リバースプロキシを起動 (自動証明書)        |
| `isola proxy stop`            | リバースプロキシを停止                           |
| `isola trust`                 | CA 証明書をシステム信頼ストアにインストール      |
| `isola open`                  | 現在の worktree をブラウザで開く                 |
| `isola doctor`                | 設定とポートの診断チェックを実行                 |
| `isola version`               | バージョン情報を表示                             |

---

## 設定リファレンス

`.isola.toml` は git リポジトリのルートに配置します。

### `[services.<name>]`

1 つ以上のサービスを定義します。各 worktree で定義された全サービスが起動されます。

| フィールド   | 型           | 必須   | 説明                                               |
| ------------ | ------------ | ------ | -------------------------------------------------- |
| `command`    | string       | はい   | サービスを起動するシェルコマンド                   |
| `dir`        | string       | いいえ | worktree ルートからの相対パス (デフォルト: ルート) |
| `port_range` | `{min, max}` | はい   | このサービスのポート割り当て範囲                   |
| `proxy_port` | int          | はい   | リバースプロキシがリッスンするポート               |

```toml
[services.frontend]
command = "pnpm run dev"
dir = "frontend"
port_range = { min = 3100, max = 3199 }
proxy_port = 3000
```

> [!IMPORTANT]
> **コマンドが割り当てポート `$PORT` をバインドするようにしてください。** isola
> は割り当てたポートを環境変数 `PORT` として注入しますが、サービス側で実際にその
> ポートをリッスンする必要があります。さもないとサービスは独自のデフォルトポートで
> 起動してしまい、isola は「誰もリッスンしていないポートで `running`」と誤って
> 報告します。
>
> 確実なのは、サービス自身に `$PORT` を読ませる方法です:
>
> - **Vite**: `vite.config.ts` で `server.port` を設定します。例:
>   `server: { port: Number(process.env.PORT) || 5173 }`。または
>   `command = "npx vite --port $PORT"` を使います。
> - **Next.js**: `command = "next dev -p $PORT"`。
> - 多くのフレームワークは `PORT` をそのまま尊重します（Rails、Django は
>   `0.0.0.0:$PORT` など）。
>
> **pnpm の注意点:** `command = "pnpm run dev -- --port $PORT"` は**動作しません**。
> pnpm が独自の `--` セパレータを挿入して `vite ... -- --port 3193` となり、Vite は
> `--` 以降をすべて位置引数として扱うため、`--port` は黙って無視され Vite は
> `5173` にフォールバックします。`npx vite --port $PORT` を使うか、上記のように
> `vite.config.ts` 内で `PORT` を読んでください。
> （[#9](https://github.com/cyucelen/isola/issues/9) 参照）

### `[env]`

全サービスに注入されるグローバル環境変数。

```toml
[env]
NODE_ENV = "development"
DATABASE_URL = "postgres://localhost/mydb"
```

### `[worktrees."<branch>"]`

worktree ごとのオーバーライド。コマンド、固定ポート、追加環境変数をカスタマイズできます。

```toml
[worktrees.main]
services.frontend.port = 3100       # main ブランチのポートを固定

[worktrees."feature/auth"]
services.backend.command = "python manage.py runserver --settings=myapp.auth 0.0.0.0:$PORT"
services.backend.env = { DEBUG = "1" }
```

---

## 環境変数

isola は以下の環境変数を全サービスプロセスに自動注入します:

| 変数                | 例                                                  | 説明                                     |
| ------------------- | --------------------------------------------------- | ---------------------------------------- |
| `PORT`              | `3117`                                              | このサービスの割り当てポート             |
| `ISOLA_BRANCH`         | `feature/auth`                                      | 現在のブランチ名                         |
| `ISOLA_BRANCH_SLUG`    | `feature-auth`                                      | ブランチ名の URL-safe スラッグ           |
| `ISOLA_SERVICE`        | `frontend`                                          | 現在のサービス名                         |
| `ISOLA_<SERVICE>_PORT` | `ISOLA_FRONTEND_PORT=3117`                             | 同一 worktree の各サービスのポート       |
| `ISOLA_<SERVICE>_URL`  | `ISOLA_BACKEND_URL=http://feature-auth.localhost:8000` | 同一 worktree の各サービスのプロキシ URL |

これにより、サービス間の通信設定を自動解決できます:

```js
// next.config.js
module.exports = {
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: `${process.env.ISOLA_BACKEND_URL}/api/:path*`,
      },
    ];
  },
};
```

---

## 仕組み

```
┌─────────────────────────────────────────────────────────────┐
│  git リポジトリ                                              │
│                                                             │
│  main worktree          feature/auth worktree               │
│  ┌───────────────┐      ┌───────────────┐                   │
│  │ frontend :3100│      │ frontend :3117│                   │
│  │ backend  :8100│      │ backend  :8104│                   │
│  └───────────────┘      └───────────────┘                   │
│         │                      │                            │
└─────────┼──────────────────────┼────────────────────────────┘
          │                      │
    ┌─────▼──────────────────────▼─────┐
    │     isola リバースプロキシ      │
    │                                  │
    │  :3000  ←  *.localhost:3000      │
    │  :8000  ←  *.localhost:8000      │
    └──────────────────────────────────┘
          │                      │
          ▼                      ▼
  main.localhost:3000    feature-auth.localhost:3000
  main.localhost:8000    feature-auth.localhost:8000
```

1. **ポート割り当て** — `FNV32(branch:service) % range` でポートを決定。再起動しても安定。
2. **プロセス管理** — サービスはプロセスグループ付きの子プロセスとして実行。ログは `.isola/logs/` に出力。
3. **リバースプロキシ** — `proxy_port` ごとに HTTP リスナーを起動。`Host` ヘッダーのサブドメインでルーティング。
4. **`*.localhost`** — [RFC 6761](https://tools.ietf.org/html/rfc6761) により、モダンブラウザは `*.localhost` を `127.0.0.1` に自動解決。DNS 設定不要。

---

## TUI ダッシュボード

![TUI Dashboard demo](./demo/demo-tui.gif)

`isola dash` で起動:

```
╭─ isola dashboard ──────────────────────────────────────────╮
│                                                               │
│  WORKTREE        SERVICE    PORT   STATUS      PID            │
│  ──────────────────────────────────────────────────────────── │
│ ▸ main           frontend   3100   ● running   12345          │
│   main           backend    8100   ● running   12346          │
│   feature/auth   frontend   3117   ○ stopped   —              │
│   feature/auth   backend    8104   ○ stopped   —              │
│                                                               │
│  Proxy: ● running (:3000, :8000)                              │
│                                                               │
│  [s] start  [x] stop  [r] restart  [o] open in browser       │
│  [a] start all  [X] stop all  [p] toggle proxy                │
│  [l] view logs  [q] quit                                      │
╰───────────────────────────────────────────────────────────────╯
```

**キーバインド:**

| キー    | 操作                     |
| ------- | ------------------------ |
| `j`/`k` | カーソル移動 (下/上)     |
| `s`     | 選択中のサービスを起動   |
| `x`     | 選択中のサービスを停止   |
| `r`     | 選択中のサービスを再起動 |
| `o`     | ブラウザで開く           |
| `a`     | 全サービス起動           |
| `X`     | 全サービス停止           |
| `p`     | プロキシの切り替え       |
| `l`     | ログファイルパスを表示   |
| `q`     | 終了                     |

---

## 使用例

```bash
# フロントエンド + バックエンドのモノレポで作業中
cd my-project

# isola を初期化
isola init
# .isola.toml を編集してサービスを定義...

# フィーチャーブランチの worktree を作成
git worktree add ../my-project-feature-auth feature/auth

# 現在のブランチのサービスを起動
isola up
# Starting frontend (port 3100) for main ...
# Starting backend (port 8100) for main ...
# ✓ 2 services started for main

# 全 worktree のサービスを一括起動
isola up --all
# ✓ 4 services started

# 状態確認
isola ls
# WORKTREE        SERVICE    PORT   STATUS    PID
# main            frontend   3100   running   12345
# main            backend    8100   running   12346
# feature/auth    frontend   3117   running   12347
# feature/auth    backend    8104   running   12348

# JSON 出力 (AI エージェントやスクリプトに最適)
isola ls --json
# [{"worktree":"main","service":"frontend","port":3100,"status":"running",
#   "pid":12345,"url":"http://main.localhost:3000","direct_url":"http://localhost:3100"}, ...]

# プロキシ起動
isola proxy start
# アクセス:
#   http://main.localhost:3000          → frontend (main)
#   http://main.localhost:8000          → backend (main)
#   http://feature-auth.localhost:3000  → frontend (feature/auth)
#   http://feature-auth.localhost:8000  → backend (feature/auth)

# HTTPS が必要な場合
isola proxy start --https
# 自動生成証明書で HTTPS プロキシを起動
# https://main.localhost:3000 でアクセス

# CA 証明書をシステムに信頼させる (ブラウザ警告を解消)
isola trust

# ブラウザで開く
isola open
# Opening http://main.localhost:3000 ...

# TUI を使う
isola dash

# 終了時
isola down --all
# ✓ 4 services stopped
```

---

## シェル補完

isola は bash、zsh、fish、PowerShell のシェル補完をサポートしています。

**bash:**
```bash
source <(isola completion bash)
# 永続化する場合:
isola completion bash > /etc/bash_completion.d/isola
```

**zsh:**
```bash
isola completion zsh > "${fpath[1]}/_isola"
# 新しいシェルを開くと有効になります。
```

**fish:**
```bash
isola completion fish | source
# 永続化する場合:
isola completion fish > ~/.config/fish/completions/isola.fish
```

**PowerShell:**
```powershell
isola completion powershell | Out-String | Invoke-Expression
# 永続化する場合:
isola completion powershell > isola.ps1
# PowerShell プロファイルに ". isola.ps1" を追加してください。
```

---

## トラブルシューティング

### サービスが起動しない

- `.isola/logs/<branch-slug>.<service>.log` のログファイルでエラー出力を確認してください。
- `.isola.toml` の `command` を手動で実行して正しく動作するか確認してください。
- `dir` で指定したディレクトリが worktree ルートからの相対パスとして存在するか確認してください。

### ポート競合

- `isola doctor` を実行してポート競合を検出してください。
- ポートが使用中の場合、isola は linear probing で範囲内の次の空きポートを探します。
- 範囲全体が使い切られた場合は、`.isola.toml` の `port_range` を広げてください。

### 古いプロセス (stale process)

- `isola doctor` を実行して state ファイル内の古い PID を検出してください。
- `isola down --all` で全サービスを停止してクリーンアップできます。
- 外部からプロセスが kill された場合、`isola ls` は自動的に `stopped` として表示します。

### プロキシが正しくルーティングしない

- `isola proxy start` でプロキシが起動しているか確認してください。
- ブラウザが `*.localhost` を解決できるか確認してください。モダンブラウザは RFC 6761 に従い自動解決します。
- 対象サービスが起動しているか `isola ls` で確認してください。
- プロキシは `Host` ヘッダーのサブドメインでルーティングするため、`http://<branch-slug>.localhost:<proxy_port>` でアクセスしてください。

### HTTPS 関連

- `isola proxy start --https` で自動生成された証明書は `.isola/certs/` に保存されます。
- ブラウザの証明書警告を解消するには `isola trust` で CA 証明書をシステムにインストールしてください。
- カスタム証明書を使う場合は `isola proxy start --cert <path> --key <path>` を指定してください。

---

## プラットフォームサポート

| プラットフォーム | ステータス | 備考 |
| -------------- | --------- | ---- |
| **macOS** | 完全対応 | 主要開発プラットフォーム |
| **Linux** | 完全対応 | Ubuntu, Debian, Fedora でテスト済み |
| **Windows** | 実験的 | 基本機能は動作。ファイルロックは代替実装を使用。問題があれば報告をお願いします |

---

## FAQ

### `*.localhost` は全てのブラウザで動きますか？

Chrome、Firefox、Edge、Safari などのモダンブラウザは [RFC 6761](https://tools.ietf.org/html/rfc6761) に従い `*.localhost` を `127.0.0.1` に解決します。`/etc/hosts` の編集や DNS 設定は不要です。

### 2 つの worktree が同じポートにハッシュされた場合は？

isola は linear probing を使用します。ハッシュで決まったポートが使用中の場合、範囲内の次の空きポートを探します。

### プロキシなしで使えますか？

はい。`isola up` でサービスを起動すれば、`localhost:<port>` で直接アクセスできます。プロキシはオプションです。

### ログはどこに保存されますか？

サービスのログは main worktree のルート配下の `.isola/logs/<branch-slug>.<service>.log` に書き出されます。

### 状態はどこに保存されますか？

ランタイム状態 (PID、ポート割り当て) は `.isola/state.json` に保存され、ファイルロックで同時アクセスの安全性を確保しています。

### ブランチごとに異なるコマンドを実行できますか？

はい。`.isola.toml` の `[worktrees."branch-name"]` でオーバーライドできます:

```toml
[worktrees."feature/auth"]
services.backend.command = "python manage.py runserver --settings=auth 0.0.0.0:$PORT"
services.backend.env = { DEBUG = "1" }
```

---

## プロジェクト構造

```
isola/
├── main.go                      # エントリーポイント
├── cmd/                         # CLI コマンド (cobra)
│   ├── root.go                  # ルートコマンド + リポジトリ/設定検出
│   ├── init.go                  # isola init
│   ├── up.go                    # isola up
│   ├── down.go                  # isola down
│   ├── ls.go                    # isola ls
│   ├── dash.go                  # isola dash
│   ├── proxy.go                 # isola proxy start|stop
│   ├── trust.go                 # isola trust
│   ├── open.go                  # isola open
│   └── version.go               # isola version
├── internal/
│   ├── cert/cert.go             # CA + サーバー証明書の自動生成
│   ├── config/config.go         # .isola.toml の読み込みとバリデーション
│   ├── git/
│   │   ├── repo.go              # リポジトリルート / common dir 検出
│   │   └── worktree.go          # worktree 一覧とブランチスラッグ
│   ├── state/store.go           # flock 付き JSON 状態永続化
│   ├── port/
│   │   ├── allocator.go         # FNV32 ハッシュベースのポート割り当て
│   │   └── registry.go          # ポート割り当て管理
│   ├── process/
│   │   ├── runner.go            # 単一プロセスのライフサイクル
│   │   └── manager.go           # マルチサービスオーケストレーション
│   ├── proxy/
│   │   ├── resolver.go          # スラッグ + ポート → バックエンド解決
│   │   └── server.go            # HTTP/HTTPS リバースプロキシ
│   ├── browser/open.go          # OS 対応のブラウザ起動
│   └── tui/                     # Bubble Tea TUI ダッシュボード
│       ├── app.go               # トップレベルモデル
│       ├── dashboard.go         # テーブルレンダリング
│       ├── keys.go              # キーバインド
│       ├── messages.go          # カスタムメッセージ
│       └── styles.go            # Lip Gloss スタイル
├── Makefile
├── .goreleaser.yaml
└── .github/workflows/
    ├── ci.yaml
    └── release.yaml
```

---

## コントリビューション

1. リポジトリをフォーク
2. フィーチャーブランチを作成 (`git checkout -b feature/amazing`)
3. 変更をコミット (`git commit -m 'feat: add amazing feature'`)
4. ブランチをプッシュ (`git push origin feature/amazing`)
5. Pull Request を作成

```bash
# 開発
make build      # バイナリをビルド
make test       # レースディテクタ付きでテスト実行
make lint       # golangci-lint を実行
make all        # fmt + vet + lint + test + build
```

---

## ライセンス

MIT License。詳細は [LICENSE](./LICENSE) を参照してください。
