# RFP: active-lens

> Generated: 2026-07-09
> Status: Draft

## 1. Problem Statement

自分が1日にどれくらい Mac を実際に操作していたかを、後から振り返れるようにしたい。
ただしキー内容やマウス座標といった「何を操作したか」は一切記録したくない
（プライバシー配慮）。「操作していた事実」と「その時間」だけが分かればよい。
さらに、単に入力していた時間（操作中）だけでなく、無操作でも画面を見ている時間
（閲覧のみ）と、離席・スリープしている時間（離席）を区別して把握したい。

対象ユーザーは開発者本人（自分専用）。完全ローカルで動作し、外部送信は行わない。

## 2. Functional Specification

### Commands / API Surface

CLI エンジン `active-lens`（Go + cgo）が計測・蓄積・集計を担い、Swift/SwiftUI 製
メニューバー GUI が `--json` を叩く薄いフロントとして可視化する
（`claude-usage-lens` / `claude-usage-lens-gui` と同じ二層構成）。

| コマンド | 役割 |
|---------|------|
| `active-lens daemon` | 常駐サンプリング。3状態を SQLite へ追記する |
| `active-lens today [--json]` | 今日の 操作中/閲覧のみ/離席 サマリ |
| `active-lens report --since <d> --until <d> [--json]` | 期間集計（GUI が叩く主インタフェース） |
| `active-lens export --format csv\|json [--since --until]` | 生サンプル/集計のエクスポート |
| `active-lens install` / `uninstall` | launchd LaunchAgent の登録・解除（ログイン時自動起動） |
| `active-lens status` | デーモン稼働状態・DBパス・直近サンプル時刻を表示 |
| `active-lens doctor` | 解決した設定値・権限・信号取得の健全性を診断 |

### 計測モデル（3状態分類）

各サンプル時点を 2 信号の組み合わせで 3 状態に分類する：

| 状態 | 判定条件 |
|------|---------|
| **操作中 (operating)** | 起きてる ＋ 画面ON ＋ アイドル秒 < 閾値 |
| **閲覧のみ (present)** | 起きてる ＋ 画面ON ＋ アイドル秒 ≥ 閾値 |
| **離席 (away)** | 画面OFF / ロック / システムスリープ |

使用信号（すべて CoreGraphics C API、cgo 経由、特別権限不要）：

- アイドル秒: `CGEventSourceSecondsSinceLastEventType`
- 画面電源: `CGDisplayIsAsleep(CGMainDisplayID())`
- ロック: `CGSessionCopyCurrentDictionary()` の `CGSSessionScreenIsLocked`
- システムスリープ: デーモンが凍結しサンプルが途切れる → サンプル間ギャップとして
  集計時に離席へ帰属させる

### 集計モデル

デーモンは生サンプル `(timestamp, state)` のみを記録する。区間集計は後段の純関数で
算出するため、閾値やギャップ上限を後から変えて再集計できる。

- 連続サンプル `a → b` の区間長 `delta = b.ts - a.ts`
- `effective = min(delta, maxGap)` を `a.state` に帰属（maxGap 既定 = 間隔×3）
- 超過分 `delta - effective` は「デーモン停止＝システムスリープ」とみなし離席へ帰属

これにより最初〜最後のサンプル間の全秒が operating/present/away に漏れなく配分される。

### Input / Output

- 入力: OS の入力アイドル・画面・ロック状態（センサ的にポーリング取得）のみ。
  ユーザーからのテキスト入力・ファイル入力はなし。
- 出力: SQLite（生サンプル）を正とし、`--json` / CSV で集計・エクスポート。
  GUI は `report --json` の結果を Swift Charts で描画（日次スタックバー、時間帯ヒートマップ、
  メニューバーの「今日の操作時間」）。

### Configuration

OS 設定ディレクトリの `config.toml`（全項目 optional、既定値あり）：

- `[sampling] interval_seconds`（既定 15）
- `[sampling] active_threshold_seconds`（既定 30）
- `[sampling] max_gap_seconds`（既定 = interval×3）
- `[storage] db_path`（既定 = データディレクトリ。iCloud Drive / Dropbox 配下を指定すれば
  緩いデバイス間同期になる）

### External Dependencies

なし。ネットワークアクセスなし。外部サービス・認証情報なし。

## 3. Design Decisions

- **CLI = Go + cgo**: util-series 標準。CoreGraphics を cgo で叩く（image-forge に cgo 実績）。
  SQLite は pure-Go の modernc.org/sqlite を使い、cgo は CoreGraphics ブリッジのみに限定。
- **GUI = Swift/SwiftUI**: ネイティブなメニューバー常駐、Swift Charts、Developer ID 署名 +
  notarize。`claude-usage-lens-gui` と同じ運用に乗せる。
- **常駐方式**: `claude-usage-lens` は launchd `StartInterval` で都度起動だが、本ツールは
  15 秒周期のため常駐デーモン（`RunAtLoad`+`KeepAlive`、`StartInterval` なし）を採用。
  都度のプロセス/cgo 初期化コストを避ける。
- **設計思想上の位置づけ**: `-lens` ファミリーの兄弟。`claude-usage-lens` が「Claude の
  使用量」を見るのに対し、`active-lens` は「Mac 操作時間」を見る。データ源が違うだけで、
  可視化・配布の作法は共通化する。
- **テスト容易性**: 信号取得を `Sampler` インタフェース背後に隔離し、状態分類・区間集計は
  純関数化。実機なしでフェイクにより検証可能にする。

### 非スコープ（明示）

1. キー内容・マウス座標の記録（恒久的に非スコープ。本ツールの根幹プライバシー原則）
2. アプリ別・ウィンドウ別の内訳（非スコープ。要件「事実と時間だけ」と整合）
3. クラウド同期・複数デバイスのマージ（機能としては作らない。DB 保存先を同期フォルダに
   置けるようにするに留める）
4. macOS 以外（darwin/arm64 専用）

## 4. Development Plan

### Phase 1: Core（CLIエンジン）

単独でレビュー可能。

- cgo で 3 信号取得（アイドル秒 / 画面電源 / ロック）、非 darwin はスタブ
- `daemon` 常駐サンプリングループ → SQLite に生サンプル追記
- 状態分類・区間集計の純関数 ＋ テスト
- `today` / `report --json` / `export` / `status` / `doctor`
- `install` / `uninstall`（launchd LaunchAgent）
- 設定: サンプリング間隔・閾値・max_gap・DB 保存先パス（config.toml）
- `go test ./...` ＋ CLI 実機 E2E（daemon で記録 → report で集計）で完結

### Phase 2: Features（GUI）

単独でレビュー可能。

- Swift/SwiftUI メニューバー常駐、署名済み CLI 同梱、`--json` で叩く薄フロント
- Swift Charts: 日次スタックバー（操作中/閲覧のみ/離席）＋時間帯ヒートマップ
- メニューバーに「今日の操作時間」表示
- GUI から daemon 有効化（launchd 登録）ワンクリック、間隔/閾値/DB パス設定 UI

### Phase 3: Release（仕上げ）

- README.md / README.ja.md / docs{en,ja} / CHANGELOG / AGENTS.md
- 署名 + notarize、util-series サブモジュール登録、org profile 追記、`check-org.sh` 緑

各 Phase は独立レビュー可能。Phase 1 は CLI 単体で「daemon で記録 → report で集計」まで
完成させ、レビュー後に Phase 2 GUI に着手する。

## 5. Required API Scopes / Permissions

**None.** アイドルポーリング方式のため Accessibility / Input Monitoring 権限は不要。
外部サービス・認証情報・ネットワークアクセスなし（完全ローカル）。

## 6. Series Placement

Series: util-series

Reason: ローカルデータの計測・集計・可視化ユーティリティであり、`claude-usage-lens` と
同じ「Go CLI エンジン + Swift GUI、Developer ID 署名 + notarize」運用に乗る兄弟ツール。

## 7. External Platform Constraints

- CoreGraphics の idle/display/lock API（権限不要だが挙動は macOS 依存）
- 自動起動は launchd LaunchAgent
- present/away の境界は「画面自動スリープまでの時間」設定に依存する。離席直後、画面が
  自動スリープするまでの数分間は一時的に「閲覧のみ」と誤分類される（限界として明記）
- 入力ベースのため「画面を見て動画視聴中で無操作」は present、完全無操作放置で画面 OFF
  後は away。「操作時間」の定義としては妥当だが仕様として明記する
- SQLite を iCloud/Dropbox 配下に置く場合、単一書き込み者（単一デバイスの daemon のみ）
  前提。複数デバイスからの同時書き込みは想定しない
- darwin/arm64 専用

---

## Discussion Log

- 当初「操作した事実と時間だけ分かればよい」という要件から、キー/マウスをフックせず
  アイドル時間をポーリングする低権限方式を提案。Accessibility/Input Monitoring 権限が
  不要な点が要件と合致。
- ユーザー要望で「無操作でも見ている時間」「操作 vs 閲覧の区別」を追加 → 画面電源・ロック
  信号を組み合わせた 3 状態モデル（操作中/閲覧のみ/離席）に拡張。
- 実現方法は `claude-usage-lens-gui` 準拠（CLI エンジン + 薄い Swift GUI）に決定。
- 常駐主体は CLI 側（`daemon`）とし、launchd で自動起動。15 秒周期のため
  `StartInterval` 都度起動ではなく常駐デーモンを採用。
- 非スコープを確定：キー内容/座標、アプリ別内訳、クラウド同期機能、macOS 以外。
  クラウド同期は「DB 保存先を同期フォルダに置ける」に留める割り切り。
- ユーザーより自律進行の承認を受領。
