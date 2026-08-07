# 実装ロードマップ

## 原則

- **縦切り**: 各フェーズは単体で動き、次を待たずに使える。データ→表示→自動供給→出力の順。
- **早くドッグフーディング**: live-pr 自身の開発を live-pr でレビューできる状態に最短で到達する。
- **難所と投機は後回し**: pivot 自動検出・マルチエージェント・凝った設定は最小で始めて後で足す。

## 確定した前提（tech-selection の未決定を初手推奨で確定）

1. 要約トリガー: `Stop` hook = セッション終端で1要約。pivot は当面 `live-pr pivot` 手動。
2. timeline ファイル: 当面 **gitignore**（ローカル runtime）。export 時に読むだけ。
3. 要約生成: `claude -p` headless。将来 API / 他エージェントに差し替え可能な interface で切る。
4. 設定: TOML（`~/.config/live-pr/config.toml` + per-repo `.live-pr/config.toml` override）。

## フェーズ

### P0 — 骨組み + データ層 ✅
- `go mod init github.com/shonenm/live-pr`、Cobra 骨組み。
- Event 型（`ts,kind,title,body,sha?`）と JSONL ストア（`.live-pr/<branch-slug>/timeline.jsonl` の read/append）。ブランチ解決。`live-pr init`。
- `live-pr append --kind … --title … --body …`、薄いラッパー `live-pr note|pivot`。
- **Done**: CLI で timeline を手で積める。`cat timeline.jsonl` で確認。
- 後回し: それ以外全部。

### P1 — 読み取り専用 TUI（体験の本実装） ✅
- Bubble Tea: 現ブランチの `conclusion.md` + `timeline.jsonl` を読む。
- GitHub PR の Conversation を模した2ペイン構成: 左に note / decision / pivot / summary の全文と commit 行を時系列表示し、右に選択した commit の `git show --stat -p` を表示。
- Open バッジ、base←head、Conversation / Files changed / Commits タブ風ヘッダ、Primer dark 配色、種別ラベル、選択アクセントバーを実装。
- `j/k`・矢印キーで選択し、選択項目が見える位置へ自動スクロール。非 commit 選択時は diff の代わりにイベント本文を表示。
- **Done**: `live-pr` が意思決定の会話と commit diff を同時に表示。fzf モックを置換。

### P2 — reviewer 起動（マイルストーン M1: 使えるレビューツール） ✅
- `tea.ExecProcess` で設定した reviewer を commit にスコープ起動（Enter）。TOML の reviewer テンプレ（`{sha}/{base}/{head}`）。suspend→実行→resume。
- **Done**: commit で Enter → nvim CodeDiff が開き、戻ってタイムライン継続。手積みタイムライン上で実レビューが回る。
- 後回し: 自動供給・export。

### P3 — git から自動供給（commit） ✅
- `live-pr sync`（TUI 起動時にも自動）: `base..HEAD` の commit を走査し timeline に upsert（sha で冪等）。hook 無しでも commit が自動で並ぶ。
- base ブランチ検出は codereview-branch の `origin/HEAD`→`main`/`master` ロジックを踏襲。
- **Done**: 実 commit がタイムラインに自動反映。decision/pivot はまだ手動。

### P4 — agent hooks（マイルストーン M2: 新規性の実証） ✅
- `live-pr hook stop`: Claude Code `Stop` hook から呼ばれ、stdin JSON の transcript path を読み、`claude -p` でセッションを要約→`summary` イベントを append。方針転換は要約プロンプト側で拾う（自動 pivot 検出は heuristic 止まり、手動 `live-pr pivot` を主とする）。
- `live-pr init --hooks` で settings.json スニペットを導入。
- **Done**: Claude セッション終了 → 要約がタイムラインに自動で乗る。**ここが差別化の核**。
- 後回し: 他エージェント adapter。

### P5 — PR export（マイルストーン M3: ループ完成） ✅
- `live-pr pr`: body = `conclusion.md`（最上部）+ 時系列の「Development timeline」節 → `gh pr create --base <base> --body-file`。既存 PR は title/body を更新。
- **Done**: 意思決定の流れを反映した PR を GitHub に出す。

### P5.1 — TUI tabs + GitHub同期基盤
- **Done**: Conversation / Files changed / Commitsを実タブ化。ローカルGitからfile/commit一覧とdiffを表示。
- **Done**: GitHub操作を共通adapterへ分離し、PR未存在と通信・認証エラーを区別。
- **Done**: PR bodyをmanaged marker内だけ更新し、外側を保持。前回publish後のremote編集・marker削除は競合として停止。
- **Done**: branch単位の`github.json`をatomic保存。cacheを即表示して起動時に1回background refreshし、以後は`r`でのみ再取得。
- **Done**: TUIの`p`から明示的にPRを作成・更新。CLIと同じpublish service、managed-body競合保護、cache更新を利用。
- **Done**: GitHubのtop-level commentsとissue activityを取得・cacheし、Conversationへ時系列統合。local eventsとGitHub commentsは枠の濃さが異なるcard、GitHub activityとGit commitsは枠なしtimeline row。source文字列は表示領域節約のため付けない。画像・動画はURL表示し、`o`で選択コメントをbrowser表示。
- **Done**: PR assigneesとlabelsを同じcache-first refreshで取得し、headerへassignee名とGitHub色のlabel pillを表示。
- **Done**: `[diff].command`をPTY/VTで右paneへ埋め込み、local PRと対話型CodeReview（`base...HEAD`）を起動時から並列表示。`Shift+Tab`/click focus、resize/input/lifecycle、raw Git・`[diff].display` fallbackに対応。
- **Next**: reviews / inline review commentsの取得とConversation統合、通常コメント投稿とoutbox。

### P6 — 仕上げ / 配布
- GitHub双方向連携の完成後、goreleaser + Homebrew tap、補完、テーマ設定、他エージェント hook adapter。dotfiles のツール登録。

## マイルストーン

| | 到達点 | 何が言えるか |
| --- | --- | --- |
| M1 (P2) | 手積みタイムライン + reviewer 起動 | ローカル PR 風レビューが回る |
| M2 (P4) | セッション要約が自動で乗る | 「意思決定タイムラインを自動供給」＝空白を埋めた |
| M3 (P5) | 流れを反映した PR export | 提唱した全ループが閉じる |

## モジュール構成（想定）

```
main.go
cmd/            cobra: root(TUI), append, note, pivot, sync, hook, pr, init
internal/
  event/        Event 型・種別・JSONL ストア
  store/        .live-pr/<branch> パス解決・conclusion
  git/          shell-out（branch/base/commits/show）
  tui/          Conversation timeline + embedded commit diff・lipgloss スタイル
  review/       reviewer テンプレ + tea.ExecProcess 起動
  summarize/    claude -p 呼び出し・プロンプト（interface で抽象化）
  pr/           body 組み立て + gh
  config/       TOML ロード
prototype/      既存 fzf モック（参照用に残す）
docs/
```

## 現在地

P0〜P5とConversation中心のTUIを`main`へ統合済み。3タブ、TUIからのPR publish、top-level GitHub commentsのMarkdown表示まで実装。次にreview/inline comment同期と通常コメント投稿を完成させ、その動作確認後にP6の配布へ進む。
