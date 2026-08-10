# 技術選定 (2026-07)

## 決定サマリ

| 層 | 採用 | 理由の要点 |
| --- | --- | --- |
| 言語 / TUI | **Go + Bubble Tea + Lipgloss + Bubbles** | gh-dash と同一スタック。単一バイナリ。サブプロセス制御が強い |
| CLI | **Cobra**（サブコマンド） | `live-pr`(TUI) と `live-pr hook/append/pivot/pr` を1バイナリに |
| ストレージ | **append-only JSONL**（XDG state配下のrepo/branch state） | 追記のみで安全。head は `conclusion.md` を上書きし、repo rootを汚さない |
| Git / GitHub | **`git` / `gh` を shell out** | go-git を持ち込まない。gh-dash と同じ流儀 |
| reviewer表示 | Portalis PTY/VT + command config | Bubble Tea右pane内でNeovim CodeReviewを常時対話操作。tmux不要 |
| エージェント連携 | Claude Code hooks → `live-pr hook` | 初手は Claude 専用。要約は `claude -p` headless |
| 設定 | **TOML**（`~/.config/live-pr/config.toml`） | Go親和。per-repo overrideは`.live-pr.toml`、旧`.live-pr/config.toml`も読む |
| 配布 | 単一バイナリ（goreleaser + Homebrew tap は後） | 依存ゼロで入る |

要は **gh-dash と同じ土台に乗る**。参照実装が豊富で、狙う見た目にも最短。

## 言語 / TUI フレームワークの比較

体験モックで求めた見た目（pin ヘッダ・種別ピル・二ペイン・タブ・タイムラインレール・スクロール・キーバインド）を fzf では出しきれない。専用 TUI が要る。

| 候補 | 長所 | 短所 | 判定 |
| --- | --- | --- | --- |
| **Go + Bubble Tea** | gh-dash と同一。単一バイナリ。`lipgloss` で Primer を容易に再現。`viewport`/`list`/`help` 完備。PTY/VT componentをpaneとして組み込める。GitHub-TUI の参照コード多数（gh-dash, glow, soft-serve）。バイナリ起動が ms 級 → hook から叩く CLI にも最適 | Go の冗長さ | ◎ 採用 |
| Rust + Ratatui | 高速・単一バイナリ | suspend/resume と外部起動が手数多い。GitHub-TUI の参照が薄い。オーバースペック | ○ 次点 |
| Python + Textual | CSS ライクで Primer 再現が速い。反復が速い | 単一バイナリでない（uv/pipx 配布）。hook から叩く CLI の起動遅延（~100-300ms） | △ |
| Node + Ink | React 的 | ランタイム依存。gh-dash 的な作り込みに不向き。dotfiles の非 JS 文化と不一致 | △ |
| fzf / bash 継続 | ゼロ依存 | レイアウト忠実度が頭打ち（モックで確認済み） | × |

決め手: **gh-dash 参照**・**単一バイナリ配布**・**外部 reviewer 起動の綺麗さ**・**hook-CLI の起動速度**。この4点すべてで Go+Bubble Tea が最良。

## アーキテクチャ（層の分離）

```
[ 物語レイヤ (本体) ]                     Go 1.22+
  $XDG_STATE_HOME/live-pr/repos/<repo-hash>/<branch-slug>/
    timeline.jsonl   append-only  {ts,kind,title,body,sha?,...}
    conclusion.md    overwrite    head（現状結論）
    github.json      atomic replace  PR binding/cache/publish baseline
    config.toml?     per-repo override

[ 供給: agent hooks ]
  Claude Code settings.json
    Stop        → live-pr hook stop        # セッション要約を1件 append
    PostToolUse → (任意) commit 検出で commit event
  手動          → live-pr pivot "…" / live-pr note "…"
  要約生成       → claude -p headless（初手）。将来 API / 他エージェント差し替え

[ 表示: TUI (Bubble Tea) ]
  live-pr        # current/local detailまたはopen PR一覧。remote PRもcheckoutなしで表示
  local git      # file/commit一覧とdiff。GitHub待ちなし
  GitHub state   # PR description + top-level comments + issue activityをcache即表示→起動時1回refresh。以後はrのみ
  Markdown       # comment本文をglamourで枠付き描画。activityは枠なし。mediaはURL表示
  embedded review # branch command / commit_commandをPTY/VT内でscope切替
  diff fallback   # scope command未設定/終了時は対応raw Git、任意で[diff].displayをstdin/stdout整形
  legacy reviewer # reviewer templateはcommit_command未設定時の互換fallback

[ 出力: PR export ]
  live-pr pr     # managed bodyだけを安全にgh pr create/edit
```

CLI 一覧（Cobra）:
`live-pr`（TUI）/ `live-pr hook stop` / `live-pr append|decision|pivot|note` / `live-pr sync` / `live-pr pr` / `live-pr init`

## 依存ライブラリ（初期）

- `github.com/charmbracelet/bubbletea` — TUI ランタイム
- `github.com/charmbracelet/lipgloss` — スタイル（Primer 配色・ピル・枠）
- `github.com/charmbracelet/bubbles` — viewport / list / help / key
- `github.com/spf13/cobra` — サブコマンド
- `github.com/BurntSushi/toml` — 設定
- `github.com/Starframe/portalis` — right paneのPTY/VT terminal（module移転中のためreplaceでcommit固定）
- 標準 `encoding/json` — JSONL
- `git` / `gh` は exec（ライブラリ不要）

## 実装で確定した事項

1. **要約トリガー**: Claude Codeの`Stop` hookでセッション終端に要約。短時間の重複実行は設定可能な間隔で抑制し、pivotは`live-pr pivot`で手動記録。
2. **timelineの扱い**: XDG state配下のユーザーruntimeとして保持し、repo rootにはruntimeファイルを作らない。旧`.live-pr/`は初回アクセス時に移行する。
3. **要約手段**: `claude -p` headlessを使用。モデルはTOML設定で上書き可能。
4. **設定形式**: TOML。グローバル設定をper-repo設定で上書き。
5. **TUI**: default branch/対象なしはPR一覧、current/local PRはdetailへ自動routing。一覧はviewer/review-request cacheを使うSaved ViewsとGitHub風filterを持ち、`baseRefName == headRefName`の確実なbranch graphだけをstack表示する。右paneは冒頭のdescription/commentをカード表示し、metadata、CI/conflict、規模をpreview。detailは固定2paneで、`b`でlocal PRを含む一覧、他PRは通常checkoutなしで開き、一覧の`c`/`x`/`m`のみ確認付きでcheckout/close/mergeする。配色はGitHub Primer darkのsemantic tokenを正本とし、gh-dash同様、通常contentはprimary/muted、PR/review/CI/merge/diff/action/labelのsemantic stateだけを着色する。
6. **GitHub refresh**: cache-first。起動時に1回だけbackground取得し、起動後は`r`またはclose/merge成功後のみrefresh。timer/daemonは持たない。
7. **PR body ownership**: `<!-- live-pr:managed:* -->`内だけlive-prが所有。外側を保持し、publish baselineとremoteが異なる場合は停止。
8. **PR publish**: CLIとTUIの`p`は同じserviceを使用。refreshからpublishは行わない。
9. **GitHub表示**: PR opening description、top-level comments、issue activityをlocal eventと時系列統合。description/commentsとlocal eventは枠の濃さで区別し、activityは枠なしrow、commitは専用pickerにのみ表示。画像・動画はURLのまま、`o`でdescription/commentをGitHub表示。PR assignees/labelsはcacheし、headerへcompact表示。
10. **Diff表示**: built-in defaultは明示的な`base...head revision`の`CodeDiff`、commitは`CodeReview`をembedded PTYで表示。他PRはnumeric pull refをnamespaced fetchし、working treeを変更しない。設定でoverrideまたは明示無効化できる。`l`で右、右の`q`で左、左の`q`で終了、`Shift+Tab`/clickでfocus切替。未設定・unsupported・終了・失敗時は対応scopeのraw Git、任意で`[diff].display`をfallback適用。

## 現在地

PR navigator、固定2pane、local/remote/commit review切替、GitHub同期基盤、TUI PR publish、top-level comment表示まで実装済み。次はreviews / inline review commentsの同期と通常コメント投稿を行い、全体確認後に配布へ進む。
