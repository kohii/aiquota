# aiquota 設計メモ（CLI フェーズ）

claude / codex / cursor のサブスクリプション usage を（ほぼ）リアルタイムに取得する CLI。
JSON 出力（機械可読）と整形表示（CodexBar 相当の見やすさ）の両方を提供する。

参考実装: CodexBar (`/Users/kohei/dev/src/github.com/steipete/CodexBar`)。ただし多機能（40+ プロバイダ・多重フォールバック・ブラウザクッキー復号）は削ぎ落とし、この Mac 環境で最も低権限・低リスクな経路だけを採る。

## 方針

- 言語: **Go 1.26**（単一バイナリ・高速・HTTP/JSON/ファイル読みが得意）。Keychain は `security` CLI 経由で cgo 不要。
- **強権限を要求しない**: 3 プロバイダすべてローカルの認証情報を読むだけで済む（下表）。Full Disk Access・ブラウザクッキー復号・Chrome Safe Storage は使わない。

| Provider | 認証情報ソース | API | 権限 |
|---|---|---|---|
| Codex | `~/.codex/auth.json`（平文 JSON, `tokens.access_token` / `account_id`） | `GET chatgpt.com/backend-api/wham/usage` (Bearer) | ファイル読み |
| Cursor | `~/Library/Application Support/Cursor/User/globalStorage/state.vscdb`（平文 SQLite, key=`cursorAuth/accessToken`） | `GET cursor.com/api/usage-summary` (+ `/api/auth/me`) | SQLite 読み（復号不要） |
| Claude | Keychain `Claude Code-credentials` (acct=`kohei`) | `GET api.anthropic.com/api/oauth/usage` (Bearer, `anthropic-beta: oauth-2025-04-20`) | Keychain 1 アイテム（起動時 1 回、以降メモリ保持） |

## アーキテクチャ

関心の分離: I/O（認証情報読み・HTTP）と純粋ロジック（パース・正規化）を分ける。

```
cmd/aiquota/            CLI エントリ（フラグ、並列フェッチ、出力切替）
internal/usage/         共通ドメインモデル（正規化スキーマ）+ Provider interface
internal/provider/codex/   auth.json 読み + wham/usage 取得 + 正規化
internal/provider/claude/  keychain 読み + oauth/usage 取得 + 正規化
internal/provider/cursor/  state.vscdb 読み + usage-summary 取得 + 正規化
internal/render/        整形表示（バー・残量・リセット時刻）
```

### Provider interface

```go
type Provider interface {
    Name() string                                  // "codex" / "claude" / "cursor"
    Fetch(ctx context.Context) (*usage.Usage, error)
}
```

各プロバイダ内部の構造:
- `credentials.go`: ローカルから token を読む（I/O。差し替え可能に）
- `client.go`: HTTP クライアント（`*http.Client` 注入可能、テストでモック）
- `parse.go`: API レスポンス → 正規化（**純粋関数**、テーブルテスト対象）

### 正規化スキーマ

CodexBar が見せている項目を横断表現する。**percent 専用にせず**、time-window 枠・コスト・残高・リクエスト数を汎用 `Meter` 配列で表す（早すぎる抽象化で実データを潰さないため）。**未知の枠も落とさず Meter として保持**する（Claude `seven_day_sonnet`/`seven_day_routines`/`seven_day_oauth_apps`、Codex `additional_rate_limits`（Spark 等）など API 側が随時追加するため）。

```go
// Meter は 1 つの計測軸（時間枠 / コスト / 残高 / リクエスト数）の汎用表現。
type Meter struct {
    Key         string     // "5h" / "weekly" / "weekly_opus" / "plan" / "on_demand" / "credits" / 未知キーそのまま
    Label       string     // 表示名
    UsedPercent *float64   // 割合がある場合 (0-100)
    Used        *float64   // 実数（usd / credits / requests）
    Limit       *float64
    Remaining   *float64
    Unit        string     // "percent" / "usd" / "credits" / "requests"
    Currency    string     // Unit=usd のとき "USD" 等
    ResetsAt    *time.Time
    WindowStart *time.Time // 枠の開始時刻。ResetsAt と併せて「現在の経過率(pace)」を算出
    Known       bool       // 既知キーにマップできたか（false=未知枠を素通し）
}

type Usage struct {
    Provider  string
    Account   string      // email 等
    Plan      string      // "pro" / "plus" / "team" ...
    Meters    []Meter     // 既知 + 未知を漏れなく
    Source    string      // "file" / "keychain" / "vscdb"
    FetchedAt time.Time
}
```

CLI は全プロバイダを並列 `Fetch`、1 つが失敗しても他は表示（部分失敗を許容）。整形表示は既知 Meter を優先配置し、未知 Meter は末尾に素直に並べる。

### ペースマーカー（CodexBar 風）

`WindowStart`/`ResetsAt` がある Meter は、使用率バー内に「現在時刻がウィンドウのどこまで進んだか」を `│` マーカーで重ねて表示する（`pace NN%` も併記）。**使用バーがマーカーより左＝時間に対して余裕、右＝使いすぎ**。`WindowStart` の供給元: Codex=`reset_at - limit_window_seconds`、Cursor=`billingCycleStart`、Claude=`resets_at - 固定窓長`（5h / 7d、開始は推定）。窓長が不明な未知枠はマーカー無し。

## プロバイダ別の詳細

### Codex
- `~/.codex/auth.json`（`CODEX_HOME` 尊重）から `tokens.access_token` / `account_id`。
- `GET chatgpt.com/backend-api/wham/usage`、`Authorization: Bearer`、`ChatGPT-Account-Id: <account_id>`。
- 正規化: `primary_window → 5h`、`secondary_window → weekly`、`credits`、`plan_type`。

### Claude
- `security find-generic-password -s "Claude Code-credentials" [-a <account>] -w` → JSON（`claudeAiOauth.accessToken` 等）。**account は固定しない**（`--claude-account` / 環境変数で指定可、未指定なら service のみで読む。CodexBar も account は optional）。
- `GET api.anthropic.com/api/oauth/usage`、`Authorization: Bearer`、`anthropic-beta: oauth-2025-04-20`、`User-Agent: claude-code/<version>`（CodexBar 準拠。UA 無しで弾かれる可能性に備える）。
- 正規化: `five_hour → 5h`、`seven_day → weekly`、`seven_day_opus → weekly_opus`、`extra_usage → コスト Meter`、`seven_day_sonnet` / `seven_day_routines` / `seven_day_oauth_apps` 等は**未知 Meter として素通し**。

### Cursor ✅ 認証経路 probe 済み（2026-05-31 実機確認）
**ブラウザクッキー復号・FDA 不要**。`state.vscdb` のローカル token だけで `usage-summary` が取れることを実機確認した（membershipType=pro 等が 200 で返却）。
- **token 取得**: `state.vscdb` を **read-only / immutable**（`file:...?immutable=1&mode=ro`）で開き `SELECT value FROM ItemTable WHERE key='cursorAuth/accessToken'`（平文 JWT, 383B）。SQLite は cgo 不要の `modernc.org/sqlite`。代替: Keychain `cursor-access-token`/`cursor-user`（WAL ロック完全回避）。
- **user_id**: 同 JWT の `sub` クレーム（例 `github|<id>`）。`~/.cursor/cli-config.json` の authId からも取れる。
- **認証ヘッダ（確定）**: `Cookie: WorkosCursorSessionToken=<sub>::<jwt>`（`::` は URL エンコードして `%3A%3A`）。← これだけが 200。Bearer も raw Cookie も 401。
- `GET cursor.com/api/usage-summary`（+ `/api/auth/me` で email）。
- 正規化: `individualUsage.plan`（`totalPercentUsed` / `autoPercentUsed` / `apiPercentUsed` / used・limit USD）、`onDemand`、`teamUsage.pooled`、legacy request plan、`billingCycleEnd → billing reset`。応答には `autoModelSelectedDisplayMessage` / `namedModelSelectedDisplayMessage`（"You've used N% ..." 文言）も含まれる。

## トークン失効の扱い

OAuth アクセストークンは失効する。基本方針は **「読むだけ」**（普段 claude/codex/cursor の CLI/IDE を使っていれば各々がファイル/Keychain を更新するため）。

- **初期版は自前 refresh を入れない。** 401/失効時は「純正 CLI / IDE で再ログインしてください」を返す。
  - 理由: refresh token はローテーションされ得るため、新 token を捨てる（メモリ保持のみ）と次回以降の純正 CLI 側が壊れる恐れ。書き戻すと純正 CLI と競合する。ワンショット CLI では「読むだけ」が最も安全。
- refresh が必要になったら **明示 opt-in**（`--refresh` / `--write-token`）で追加。その際 Codex: `auth.openai.com/oauth/token` + client_id `app_EMoamEEZ73f0CkXaXp7hrann`、Claude: Anthropic OAuth endpoint（client_id は CodexBar から要確認）。

## HTTP クライアントの責務

provider client 共通で最初から持たせる:
- プロバイダごとの必須ヘッダ（Claude: `anthropic-beta` + `User-Agent: claude-code/<ver>`、Codex: `ChatGPT-Account-Id` + `User-Agent`）。
- 429 は `Retry-After` を見て backoff（CodexBar 準拠）。タイムアウト・1 回程度のリトライ。
- 401/403 は「再ログイン要」エラーに正規化（refresh 方針と連動）。

## CLI

- `aiquota`               → 整形表示（全プロバイダ）
- `aiquota --json`        → JSON 配列
- `aiquota <provider>`    → 単一プロバイダ
- 終了コード: 全失敗で非 0、部分成功は 0（表示にエラー併記）。

## テスト

- `parse.go` は純粋関数 → テーブルテスト。fixture は**最初から**: Codex `additional_rate_limits`（Spark 等）、Claude 追加 windows（`seven_day_sonnet`/`seven_day_routines` 等）、Cursor Enterprise/Team/legacy request plan を含める。
- HTTP は `*http.Client` / interface 注入で `httptest` モック。
- 認証情報読みは interface 化し、実 Keychain/ファイルに触れない単体テスト。
- 実認証情報での疎通は手動/opt-in（環境変数ガード）。

## 実装の進め方（順序）

0. ~~Cursor 認証 probe~~ ✅ 完了（`WorkosCursorSessionToken=<sub>::<jwt>` で 200 確認）。3 プロバイダすべて低権限で取得可能と確定。
1. 正規化スキーマ（`Meter`/`Usage`）と `Provider` interface を確定。
2. **Codex** から実装（最も簡単・ファイルのみ）→ `--json` と整形表示。
3. **Claude** 実装（Keychain 読み + OAuth API、401 は再ログイン要を返す）。
4. **Cursor** 実装（step 0 が通った場合）。
5. 整形表示を CodexBar 相当に寄せる。

## 未確定・リスク

1. ~~Cursor accessToken が API session として使えるか~~ → ✅ 解決（`Cookie: WorkosCursorSessionToken=<sub>::<jwt>`）。
2. **Claude refresh の client_id / endpoint** → refresh を入れる段階で CodexBar から確認（初期版は不要）。
3. state.vscdb の immutable 読みが Cursor の WAL 更新中に古い値を返す可能性 → 許容（usage は数分粒度）。または Keychain 経路を優先。
4. レスポンススキーマは API 仕様非公開のため変化しうる → パースは寛容に（欠損は nil 許容、未知枠は素通し）。

## GUI フェーズ（後続・未着手）

CLI をコア（`internal/` をライブラリ化 or `--json` を叩く）に、menubar GUI を被せる。Swift ネイティブか Go(systray) かは CLI 完成後に判断。CLI とは疎結合を保つ。
