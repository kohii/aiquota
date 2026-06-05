# aiquota 設計メモ

claude / codex / cursor / copilot のサブスクリプション usage を（ほぼ）リアルタイムに取得する CLI。
JSON 出力（機械可読）と整形表示（CodexBar 相当の見やすさ）の両方を提供する。

参考実装: CodexBar (`/Users/kohei/dev/src/github.com/steipete/CodexBar`)。ただし多機能（40+ プロバイダ・多重フォールバック・ブラウザクッキー復号）は削ぎ落とし、この Mac 環境で最も低権限・低リスクな経路だけを採る。

## 方針

- 言語: **Go 1.26**（単一バイナリ・高速・HTTP/JSON/ファイル読みが得意）。Keychain は `security` CLI 経由で cgo 不要。
- **強権限を要求しない**: 4 プロバイダすべてローカルの認証情報を読むだけで済む（下表）。Full Disk Access・ブラウザクッキー復号・Chrome Safe Storage は使わない。

| Provider | 認証情報ソース | API | 権限 |
|---|---|---|---|
| Codex | `~/.codex/auth.json`（平文 JSON, `tokens.access_token` / `account_id`） | `GET chatgpt.com/backend-api/wham/usage` (Bearer) | ファイル読み |
| Cursor | `~/Library/Application Support/Cursor/User/globalStorage/state.vscdb`（平文 SQLite, key=`cursorAuth/accessToken`） | `GET cursor.com/api/usage-summary` (+ `/api/auth/me`) | SQLite 読み（復号不要） |
| Claude | Keychain `Claude Code-credentials` (acct=`kohei`) | `GET api.anthropic.com/api/oauth/usage` (Bearer, `anthropic-beta: oauth-2025-04-20`) | Keychain 1 アイテム（起動時 1 回、以降メモリ保持） |
| Copilot | `~/.config/github-copilot/apps.json`（平文 JSON, `<host:appId>.oauth_token`、`XDG_CONFIG_HOME` 尊重） | `GET api.github.com/copilot_internal/user`（`Authorization: token`） | ファイル読み |

## アーキテクチャ

関心の分離: I/O（認証情報読み・HTTP）と純粋ロジック（パース・正規化）を分ける。

```
cmd/aiquota/            CLI エントリ（フラグ、並列フェッチ、出力切替）
internal/usage/         共通ドメインモデル（正規化スキーマ）+ Provider interface
internal/provider/codex/   auth.json 読み + wham/usage 取得 + 正規化
internal/provider/claude/  keychain 読み + oauth/usage 取得 + 正規化
internal/provider/cursor/  state.vscdb 読み + usage-summary 取得 + 正規化
internal/provider/copilot/ apps.json 読み + copilot_internal/user 取得 + 正規化
internal/render/        整形表示（バー・残量・リセット時刻）
```

### Provider interface

```go
type Provider interface {
    Name() string                                  // "codex" / "claude" / "cursor" / "copilot"
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
    Unlimited   bool       // プロバイダが「上限なし」と報告する枠（Copilot Chat/Completions 等）。UsedPercent=nil とは別概念で、消費側が「percent 未報告」を unlimited と誤推論しないための明示フラグ
    Known       bool       // 既知キーにマップできたか（false=未知枠を素通し）
}

type Usage struct {
    Provider  string
    Account   string      // email / login 等。整形表示では伏せ、--json のみ出力
    Plan      string      // "pro" / "plus" / "team" ...
    Meters    []Meter     // 既知 + 未知を漏れなく
    Source    string      // "file" / "keychain" / "vscdb"
    FetchedAt time.Time
}
```

CLI は全プロバイダを並列 `Fetch`、1 つが失敗しても他は表示（部分失敗を許容）。整形表示は既知 Meter を優先配置し、未知 Meter は末尾に素直に並べる。

### ペースマーカー（CodexBar 風）

`WindowStart`/`ResetsAt` がある Meter は、使用率バー内に「現在時刻がウィンドウのどこまで進んだか」を `│` マーカーで重ねて表示する（`pace NN%` も併記）。**使用バーがマーカーより左＝時間に対して余裕、右＝使いすぎ**。`WindowStart` の供給元: Codex=`reset_at - limit_window_seconds`、Cursor=`billingCycleStart`、Claude=`resets_at - 固定窓長`（5h / 7d、開始は推定）、Copilot=`quota_reset_date_utc - 1ヶ月`（毎月 1 日 00:00 UTC リセット）。窓長が不明な未知枠はマーカー無し。

### 着色（ペース基準＝use-it-or-lose-it）

固定サブスクの枠は「使い切れないと損」なので、色は**絶対使用率ではなく「着地予測」**で決める。着地予測 = `pct / pace × 100`（今のペースでリセット時に最終何%使うかの線形予測。バーの塗りがマーカーより左に大きく離れている＝余裕＝損、を色でも強調する）。`pace >= overrunFloor(20%)` 経過してから予測を信頼し、それ以前や窓無しの枠は絶対使用率にフォールバックする（時計が無い／早すぎて損とは言えないため、低使用は緑のまま）。`levelOf(pct, pace)` を**唯一の分類関数**とし、ANSI 色と信号絵文字の両方をここから導出する（旧 `colorByPct`/`signalEmoji` の閾値二重管理を解消）。

| level | 条件 | 色 / 絵文字 | 意味 |
|---|---|---|---|
| Loss | `pace>=25%` かつ 着地予測 `<60%` | 青(34) / 🔵 | 使い切れず損。もっと使え |
| Good | 上記以外で着地予測 ~`[60,110)%` | 緑(32) / 🟢 | ちょうど良いペース |
| Warn | 着地予測 `>=110%` | 黄(33) / 🟡 | やや使いすぎ・やや早く枯渇 |
| Crit | 絶対 `pct>=85%` または 着地予測 `>=140%` | 赤(31) / 🔴 | 今ほぼ枯渇 or 大きく早く枯渇 |

- **安全網**: `pct>=85%`（今ほぼ使い切り）は pace を問わず常に Crit。窓が浅い間（`pace<20%`）も `pct>=60%→Warn / >=85%→Crit` の絶対フォールバックで過剰消費は拾う。一方 Loss(青)は早すぎる序盤ノイズを避けるため `pace>=25%` まで出さない（赤は早め・青は慎重、という非対称）。
- **着色対象**は `UsedPercent != nil` の枠のみ＝各社のサブスク%枠（claude 5h/weekly、codex 5h/weekly、cursor plan%、copilot premium）で全て use-it-or-lose-it。コスト系（cursor on_demand `$`、codex credits）は percent 無しで対象外なので「もっと使え＝青」が誤発火しない。
- **`proj NN%`** を行末に併記し、色の根拠（着地予測）を読めるようにする（`pace>=20%` のときのみ）。`pace=0`/NaN/Inf は `usablePace` でガードしフォールバック。Loss(青)時は pace マーカーを太字シアン→**明るい白(1;97)** に切替え、青バー上での視認性を確保。

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

### Copilot ✅ 認証経路 probe 済み（2026-05-31 実機確認）
**Keychain・FDA 不要**。GitHub Copilot の各 editor プラグイン（VS Code / Neovim / `gh` 拡張）が書く平文トークンファイルを読むだけ。CodexBar は自前 Device Flow ログインで独自 Keychain に保存するが、本ツールは「既存ファイルを読むだけ」の最小権限経路を採る。
- **token 取得**: `~/.config/github-copilot/apps.json`（`XDG_CONFIG_HOME` 尊重）。`{"<host>:<appId>": {"oauth_token": "ghu_…", "user": "…"}}` 形式。`github.com` ホストを優先、無ければ任意の有効トークンへフォールバック（enterprise 単一ホスト想定）。トークンは GitHub App user-to-server token（`ghu_` プレフィックス）。
- **認証ヘッダ（確定）**: `Authorization: token <oauth_token>`（Bearer ではなく `token` スキーム）。`copilot_internal` は editor クライアントを装ったヘッダ（`Editor-Version` / `Editor-Plugin-Version` / `User-Agent: GitHubCopilotChat/…` / `X-Github-Api-Version`）を付ける。
- `GET api.github.com/copilot_internal/user`。`login` / `copilot_plan`（business / pro / free / enterprise）/ `quota_reset_date_utc` を取得。
- 正規化: `quota_snapshots` の各枠（`premium_interactions` / `chat` / `completions`、未知キーは素通し）。`unlimited:true` の枠はマーカー行（パーセント無し・ラベルに "(unlimited)"）、metered な枠は `percent_remaining → UsedPercent`（無ければ `entitlement`/`remaining` から導出）、`entitlement → Limit`、`quota_remaining → Remaining`（精度の高い float を優先、無ければ int の `remaining`）、`Used = Limit - Remaining`、Unit=`requests`。全枠が毎月一斉リセット。

## トークン失効の扱い

OAuth アクセストークンは失効する。基本方針は **「読むだけ」**（普段 claude/codex/cursor の CLI/IDE を使っていれば各々がファイル/Keychain を更新するため）。

- **初期版は自前 refresh を入れない。** 401/失効時は「純正 CLI / IDE で再ログインしてください」を返す。
  - 理由: refresh token はローテーションされ得るため、新 token を捨てる（メモリ保持のみ）と次回以降の純正 CLI 側が壊れる恐れ。書き戻すと純正 CLI と競合する。ワンショット CLI では「読むだけ」が最も安全。
- refresh が必要になったら **明示 opt-in**（`--refresh` / `--write-token`）で追加。その際 Codex: `auth.openai.com/oauth/token` + client_id `app_EMoamEEZ73f0CkXaXp7hrann`、Claude: Anthropic OAuth endpoint（client_id は CodexBar から要確認）。

## HTTP クライアントの責務

provider client 共通で最初から持たせる:
- プロバイダごとの必須ヘッダ（Claude: `anthropic-beta` + `User-Agent: claude-code/<ver>`、Codex: `ChatGPT-Account-Id` + `User-Agent`、Copilot: `Authorization: token` + editor 偽装ヘッダ群）。
- 429 は `Retry-After` を見て backoff（CodexBar 準拠）。タイムアウト・1 回程度のリトライ。
- 401/403 は「再ログイン要」エラーに正規化（refresh 方針と連動）。

## CLI

- `aiquota`               → 整形表示（全プロバイダ）。ヘッダは `provider · plan` のみで、`Account`（email/login）は共有端末やスクショでの漏洩を避けるため**伏せる**
- `aiquota --json`        → JSON 配列（`Account` も含むフル情報）
- `aiquota <provider>`    → 単一プロバイダ
- **未設定（未導入・未ログイン）の区別**: 認証情報ソースが存在しない場合は `NotConfiguredError` を返し、`⚠` ではなく淡色の `– not configured` 行で静かに表示。終了コードにも影響しない（ツール未導入の環境はエラーではない）。`--json` では `{"notConfigured": true}`。判定: Codex/Copilot=ファイル不在 or トークン空、Cursor=`state.vscdb` 不在 or token 行なし、Claude=`security` の終了コード 44（errSecItemNotFound）。
- 終了コード: **本当のエラー（parse 失敗・401・ネットワーク等）があり、かつ成功が 0 件のとき**のみ非 0。部分成功は 0、未設定のみも 0。

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
6. **Copilot** 追加（`apps.json` 読み + `copilot_internal/user`、`requests` 単位の枠表示）。

## 未確定・リスク

1. ~~Cursor accessToken が API session として使えるか~~ → ✅ 解決（`Cookie: WorkosCursorSessionToken=<sub>::<jwt>`）。
2. **Claude refresh の client_id / endpoint** → refresh を入れる段階で CodexBar から確認（初期版は不要）。
3. state.vscdb の immutable 読みが Cursor の WAL 更新中に古い値を返す可能性 → 許容（usage は数分粒度）。または Keychain 経路を優先。
4. レスポンススキーマは API 仕様非公開のため変化しうる → パースは寛容に（欠損は nil 許容、未知枠は素通し）。

## GUI フェーズ

### Raycast Script Command + `--style emoji` ✅

当初は Script Command の `fullOutput` が ANSI 色を解釈せず見にくかったため React/TS のネイティブ拡張を試作したが、検討の結果**撤去した**。拡張は `List` で 1 Meter=1 カードになり、CLI が持つ「等幅バー＋ pace マーカー（使用率 vs 経過率の一目比較）＋高密度で全枠を一画面俯瞰」という良さを失っていた。pace と縦圧縮はそもそも CLI 側に揃っており、プレーンテキストで失われるのは「色（警戒度の一目把握）」だけだった。

そこで色を**信号絵文字で代替**する出力スタイルを CLI に追加した（`--style emoji`）。`fullOutput` は等幅プレーンテキストで ANSI を無視するが絵文字はカラー表示されるため、行頭の 🔵🟢🟡🔴（CLI の ANSI 色と同じ `levelOf` 由来＝🔵 使い切れず損 / 🟢 ちょうど / 🟡 やや使いすぎ / 🔴 枯渇間近、上限なし枠と未報告は ⚪）で状態を伝えられる。全行が同一幅の絵文字＋スペースで始まるので、絵文字が全角でもバーの相対アラインメントは崩れない。

- **`internal/render` が唯一の真実の源**: `Render` は `Options{Color, Emoji}` を取る。`auto`=TTY なら ANSI 色、`plain`=無装飾、`emoji`=信号絵文字（ANSI なし）。色も絵文字も `levelOf`（着地予測ベース）から導出し、pace / reset 整形 / 閾値を TS で再実装する二重管理を解消した。
- **配布**: `~/dotfiles/raycast/scripts/aiquota.sh` が `aiquota --style emoji` を `exec` するだけ。Raycast に限らず Alfred / SwiftBar / xbar でも同じ。バイナリは PATH（`~/go/bin` 等）で解決。
- Claude の Keychain 読み取りは `security` を**絶対パス `/usr/bin/security`** で叩く。Raycast 等のランチャーは `/usr/bin` を含まない最小 PATH で子プロセスを起動するため、PATH 依存だと `executable file not found` で落ちていた。

### menubar GUI（未着手）

常時表示が欲しくなれば menubar（SwiftBar/xbar 対応 or Swift ネイティブ）を被せる余地はある。いずれも `--json` を叩く疎結合を保つ。
