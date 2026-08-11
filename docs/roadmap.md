# 実装ロードマップ

## 原則

- **縦切り**: 各フェーズは単体で動き、次を待たずに使える。データ→表示→自動供給→出力の順。
- **早くドッグフーディング**: live-pr 自身の開発を live-pr でレビューできる状態に最短で到達する。
- **難所と投機は後回し**: pivot 自動検出・マルチエージェント・凝った設定は最小で始めて後で足す。

## 確定した前提（tech-selection の未決定を初手推奨で確定）

1. 要約トリガー: `Stop` hook = セッション終端で1要約。pivot は当面 `live-pr pivot` 手動。
2. timeline ファイル: 当面 **gitignore**（ローカル runtime）。export 時に読むだけ。
3. 要約生成: `claude -p` headless。将来 API / 他エージェントに差し替え可能な interface で切る。
4. 設定: TOML（`~/.config/live-pr/config.toml` + per-repo `.live-pr.toml` override、旧パスも移行用に読込）。

## フェーズ

### P0 — 骨組み + データ層 ✅
- `go mod init github.com/shonenm/live-pr`、Cobra 骨組み。
- Event 型（`id,ts,kind,title,body,author,sha?`）と追記型JSONLストア（XDG state配下のrepo/branch state）。ブランチ解決。`live-pr init`。
- `live-pr comment add/list/edit/delete`と薄いラッパー`live-pr note|decision|pivot`。edit/deleteは履歴を書き換えずoperation/tombstoneを追記する。
- **Done**: 人間とagentがstable IDでreviewer向けtimelineを操作でき、legacy recordも編集可能。

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
- `live-pr hook stop`: Claude Code `Stop` hook から呼ばれ、reviewerに必要な意思決定・pivot・制約・重要指摘がある場合だけ`summary`イベントをappendする。通常の進捗・test修正・cleanupは記録しない。
- `live-pr init --hooks` で settings.json スニペットを導入。
- **Done**: agent sessionから重要な判断だけを疎なtimelineへ供給。手動`live-pr pivot`を確実な経路とする。
- 後回し: 他エージェント adapter。

### P5 — PR export（マイルストーン M3: ループ完成） ✅
- `live-pr pr`: body = `conclusion.md`（最上部）+ 時系列の「Development timeline」節 → `gh pr create --base <base> --body-file`。既存 PR は title/body を更新。
- **Done**: 意思決定の流れを反映した PR を GitHub に出す。

### P5.1 — TUI + GitHub同期基盤
- **Done**: tabを廃止し、左Conversation・右Files changed/CodeReviewを固定表示。`c`で左をcommit pickerへ切替え、`Enter`で右をcommit scopeへ切替。`Esc`でbranch scopeへ戻る。
- **Done**: GitHub操作を共通adapterへ分離し、PR未存在と通信・認証エラーを区別。
- **Done**: PR bodyをmanaged marker内だけ更新し、外側を保持。前回publish後のremote編集・marker削除は競合として停止。
- **Done**: branch単位の`github.json`をatomic保存。cacheを即表示して起動時に1回background refreshし、以後は`r`でのみ再取得。
- **Done**: TUIの`p`から明示的にPRを作成・更新。CLIと同じpublish service、managed-body競合保護、cache更新を利用。
- **Done**: GitHubのPR opening description、top-level comments、issue activityをcacheし、Conversationへ時系列統合。opening descriptionとcommentsはcloud card、local eventsは濃さの異なるcard、GitHub activityは枠なしtimeline row、Git commitsは専用pickerにのみ表示。画像・動画はURL表示し、`o`で選択したdescription/commentをGitHubで開く。
- **Done**: PR assigneesとlabelsを同じcache-first refreshで取得し、headerへassignee名とGitHub色のlabel pillを表示。
- **Done**: built-inのbranch three-dot `CodeDiff` / commit `CodeReview`をPTY/VTで右paneへ埋め込み、設定overrideと明示無効化、`l`/`q`/`Shift+Tab`/click focus、resize/input/lifecycle、raw Git・`[diff].display` fallbackに対応。
- **Done**: GitHub Primer darkとgh-dashのsemantic color運用へ統一し、独自event-kind色を廃止。
- **Done**: PR一覧にAll/Review requested/Assigned/Authored/Needs me view、GitHub風filter、base/head graphによるstack groupingとcollapseを追加。
- **Done**: default branch/対象なしではcache-firstのPR一覧、current/local PRではdetailを自動表示。`b`でlocal PRを含む一覧へ戻り、右previewへ冒頭のdescription/commentカード、metadata、CI/conflict/review、comments、files/lines/commitsを表示。他PRは通常checkoutせずnumeric pull refをfetchしてConversationとCodeReviewを表示し、一覧で`c`によるcheckout、`x`によるclose、`m`によるmergeを確認後に実行可能。
- **Next**: reviews / inline review commentsの取得とConversation統合、通常コメント投稿とoutbox。
- **Done**: CodeReviewのreviewed markをXDG stateへ保存し、チェック後に対象ファイルのdiffが変わった場合は自動解除。
- **Done**: repo-local `.live-pr/` runtimeを廃止し、XDG stateへ移行。旧stateは初回アクセス時に移行。

### P5.2 — Local PR authoring + Agent Skill ✅
- **Done**: repositoryのdefault PR templateからfinal summaryをseedし、CLIまたはTUIの`e`で更新。local TUIのConversation先頭へ表示する。
- **Done**: TUIの`a/e/d`でlocal commentを追加・編集・削除し、`Ctrl+S`で保存。remote commentとcommitは編集対象外。
- **Done**: Agent Skills Standardの`skills/live-pr/SKILL.md`をrepo配布し、binaryにもembed。`gh skill install`と`live-pr skill path/print`の両経路を提供。
- **Done**: Skillは初期計画ではなく最終結果をsummaryへ書き、reviewerが必要とする重要判断だけをtimelineへ記録する。

### P6 — 仕上げ / 配布
- GitHub双方向連携の完成後、Homebrew tap、テーマ設定、追加agent adapter。goreleaser・補完・version-matched Agent Skillは導入済み。

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
  git/          shell-out（branch/base/range/pull-ref fetch）
  tui/          Conversation timeline + embedded commit diff・lipgloss スタイル
  review/       reviewer テンプレ + tea.ExecProcess 起動
  summarize/    claude -p 呼び出し・プロンプト（interface で抽象化）
  pr/           body 組み立て + gh
  config/       TOML ロード
prototype/      既存 fzf モック（参照用に残す）
docs/
```

## 現在地

P0〜P5.2とConversation中心のTUIを実装。Local PRはPR template準拠のfinal summaryと、人間/agentがCLIでCRUDできる疎なdecision timelineを持ち、version一致Skillから運用規則をagentへ提供する。次はGitHub review/inline comment同期と通常コメント投稿outbox、Homebrew配布。
