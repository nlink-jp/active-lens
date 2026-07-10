# active-lens

Mac を実際にどれくらい操作していたかを記録・可視化する。ただし**「何を操作したか」は
一切記録しない**。

active-lens は OS から内容を含まない活動シグナル（最後の入力からの経過秒・画面電源・
画面ロック）をポーリングし、1 日を 3 状態に分類します。

- **操作中 (operating)** — 起きてる・画面ON・閾値以内に入力あり
- **閲覧のみ (present)** — 起きてる・画面ONだが直近の入力なし（視聴・閲覧）
- **離席 (away)** — 画面OFF・ロック・システムスリープ

キーストローク・マウス座標・最前面アプリなどは一切見ません。Accessibility や
Input Monitoring の権限は不要で、ネットワークアクセスもありません。

本リポジトリは CLI エンジンです。データを可視化するメニューバー GUI（Swift）は姉妹
プロジェクトで、`claude-usage-lens` / `claude-usage-lens-gui` と同じ構成です。

> **対応環境:** macOS / Apple Silicon（darwin/arm64）専用。

## 仕組み

常駐デーモンが _interval_ 秒ごとに活動状態をサンプリングし、生の
`(timestamp, state)` 行をローカル SQLite に追記します。集計は生サンプルから算出する
ため、閾値やギャップ上限を後から変えて再集計できます。

連続する 2 サンプル間の時間は、手前のサンプルの状態に `max_gap` を上限として帰属し
ます。それを超えるギャップはデーモンが動いていなかった（システムスリープ）ことを
意味し、超過分は **離席** に帰属します。最初と最後のサンプル間の全秒が漏れなく配分
されます。

## インストール

```sh
make build            # -> dist/active-lens （go build は直接使わない）
cp dist/active-lens ~/bin/active-lens   # PATH の通った場所へ
```

ログイン時のバックグラウンド計測を有効化：

```sh
active-lens install   # daemon を実行する launchd LaunchAgent を登録
```

`install` は現在のバイナリのパスを LaunchAgent に登録します。配置したい場所から
インストールしてください。

## 使い方

```
active-lens daemon                 常駐サンプラー（通常は launchd 経由）
active-lens now      [--json]      いま続いているセッション
active-lens today    [--json]      今日の 操作中/閲覧のみ/離席 合計
active-lens timeline [flags]       勤務ログ：始業/終業/休憩を日別に
active-lens report   [flags]       期間集計
active-lens export   [flags]       生サンプルのエクスポート（--format csv|json）
active-lens status   [--json]      デーモン状態・DBパス・直近サンプル
active-lens doctor                 設定・シグナル・デーモンの健全性診断
active-lens install | uninstall    ログイン用 LaunchAgent の登録/解除
active-lens version
```

### セッションと論理日

**セッション**とは、途切れずに続いた一連の作業です。`work.session_gap_minutes`
（既定 4 時間）以上の離席——一晩の睡眠、午後の外出——でセッションは終わります。
それより短い離席はセッションの内側に留まり、`work.break_minutes`（既定 10 分）
以上のものが**休憩**、それより短いものは連続稼働に畳み込まれます。*操作中* と
*閲覧のみ* の両方を「在席」とみなします。

セッションは深夜 0 時で分割されません。**論理日**——00:00 ではなく
`work.day_start_hour`（既定 04:00）に始まる日——のうち、それが始まった日へ、
丸ごと帰属します。ですから 00:59 まで続いた夜はその夜のものであり、翌朝は
実際に机に向かった時刻から始まります。セッション間の睡眠は休憩ではなく、
午前と夜のセッションを隔てる 5 時間の空白もまた休憩ではありません。

### now（いまのセッション）

```sh
active-lens now
active-lens now --json      # GUI のメニューバー向け
```

```
Session  20:44 → 00:59   (open)
  active     3h 53m   (operating 3h 01m, present 52m)
  breaks     2 · 22m
               21:51–22:01 (10m)
               22:30–22:41 (11m)
  today      3h 53m   (2026-07-09)

Currently operating · recording
```

直近の活動からセッションギャップ未満しか経っていなければセッションは `open`、
open だが今まさに離席中なら `paused` です。離席から 30 分の時点では、それが休憩なのか
一日の終わりなのかまだ誰にも言えないので、セッションは open のままです。離席が
ギャップを超えた時点で closed になります。セッションの**開始時刻は決して変わりません**。
変わるのは `open` だけです。

### timeline（勤務ログ）

生サンプルから、論理日ごとに **いつ** マシンに向かっていたか——各セッションの始業・
休憩・終業——を再構成します。

```sh
active-lens timeline                                   # 直近7日
active-lens timeline --days 30                         # 直近30論理日
active-lens timeline --since 2026-07-01 --until 2026-07-08
active-lens timeline --json                            # GUI 向け
```

```
2026-07-09   20:44 → 00:59 (+1d)   active 3h 53m   · 2 break(s) 22m
    break 21:51–22:01 (10m)
    break 22:30–22:41 (11m)

2026-07-10   07:26 → 10:42   active 2h 40m
```

`(+1d)` は深夜 0 時をまたいで終わったセッションを示します。`--json` 出力には各日の
色付きスパン（タイムライン表示用）、`sessions` と `blocks`、そして導出した
`work_start` / `work_end` / `breaks` が含まれます。`--since` の日付を自分で計算するより
`--days N` を使ってください。境界を含めて論理日で範囲を解決します。

### report / today

```sh
active-lens today
active-lens report --since 2026-07-01 --until 2026-07-08
active-lens report --json           # GUI 向けの機械可読出力
```

`--until` は当日を含みます。フラグ無しの `report` は直近 7 日間を対象にします。
日バケットは論理日なので、01:00 に叩いた `today` は、いま自分がその只中にいる夜を
そのまま報告します。時間帯ヒートマップは実時刻の時間帯を保ちます。

なお `report` は各**秒**をそれが属する論理日へ、`timeline` は各**セッション**を
それが始まった日へ帰属させます。両者はセッションが `day_start_hour` を貫くときにのみ
食い違い、徹夜は `timeline` では開始日に全量が乗り、`report` では 2 日に分かれます。
これは稼働ログと合計台帳の差です。

出力例：

```
Timezone: Asia/Tokyo    Range: 2026-07-02 → 2026-07-09    (41210 samples)

Total    operating 38h 12m   present 9h 05m   away 120h 43m

By day
  2026-07-09   operating 6h 10m   present 1h 20m   away 16h 30m
  ...
```

### export

```sh
active-lens export --format csv  --since 2026-07-01 > activity.csv
active-lens export --format json --since 2026-07-01 > activity.json
```

## 設定

`~/Library/Application Support/active-lens/` に任意の `config.toml`
（[`config.example.toml`](config.example.toml) 参照）。全キー optional：

| キー | 既定 | 意味 |
|-----|------|------|
| `sampling.interval_seconds` | `15` | デーモンのサンプリング間隔 |
| `sampling.active_threshold_seconds` | `30` | 操作中/閲覧のみ を分けるアイドル閾値 |
| `sampling.max_gap_seconds` | `interval × 3` | ギャップ上限。超過は離席（スリープ）扱い |
| `work.break_minutes` | `10` | セッション内で休憩とみなす最小離席（分） |
| `work.session_gap_minutes` | `240` | セッションを終わらせる離席（分）。`break_minutes` より大きいこと |
| `work.day_start_hour` | `4` | 論理日が始まるローカル時刻。`0` でカレンダー日 |
| `storage.db_path` | データディレクトリ | サンプル保存先。iCloud/Dropbox 配下で緩い同期 |

解決値は `active-lens doctor` で確認できます。

## プライバシー

active-lens が読むのは次の 3 つだけです：

- 最後の入力イベントからの経過秒（数値ひとつ）
- 画面がスリープ中か（真偽値）
- 画面がロック中か（真偽値）

キーコード・座標・ウィンドウ/アプリ名・その他いかなる内容も読みも保存もしません。
データベースにはタイムスタンプと 3 状態のラベルのみが入ります。データが端末外へ
出ることはありません。

## 制限

- 入力ベースのため、無操作での動画視聴は **閲覧のみ**、完全に無操作で画面が
  スリープすると **離席** になります。
- 離席直後、画面が自動スリープするまでの間は一時的に **閲覧のみ** と分類されます。
  これは省エネ設定の画面スリープ時間に依存します。
- DB を iCloud/Dropbox 配下に置く場合、単一の書き込み者（1 台のデーモン）を前提と
  します。複数デバイスからの同時書き込みは想定しません。

## 開発

```sh
make test    # go test ./...
make vet     # darwin(cgo) + linux スタブのコンパイル確認
```

cgo が必要です（CoreGraphics シグナルブリッジ）。SQLite は pure-Go
（modernc.org/sqlite）なので、cgo はその 1 パッケージに限定されます。

## ライセンス

MIT — [LICENSE](LICENSE) 参照。
