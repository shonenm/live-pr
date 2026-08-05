# 技術選定 (2026-07)

## 決定サマリ

| 層 | 採用 | 理由の要点 |
| --- | --- | --- |
| 言語 / TUI | **Go + Bubble Tea + Lipgloss + Bubbles** | gh-dash と同一スタック。単一バイナリ。サブプロセス制御が強い |
| CLI | **Cobra**（サブコマンド） | `live-pr`(TUI) と `live-pr hook/append/pivot/pr` を1バイナリに |
| ストレージ | **append-only JSONL**（`.live-pr/<branch>/`） | 追記のみで安全。head は `conclusion.md` を上書き |
| Git / GitHub | **`git` / `gh` を shell out** | go-git を持ち込まない。gh-dash と同じ流儀 |
| reviewer 起動 | コマンドテンプレ + `tea.ExecProcess` | TUI を suspend→nvim 等を実行→resume |
| エージェント連携 | Claude Code hooks → `live-pr hook` | 初手は Claude 専用。要約は `claude -p` headless |
| 設定 | **TOML**（`~/.config/live-pr/config.toml`） | Go 親和。per-repo override 可 |
| 配布 | 単一バイナリ（goreleaser + Homebrew tap は後） | 依存ゼロで入る |

要は **gh-dash と同じ土台に乗る**。参照実装が豊富で、狙う見た目にも最短。

## 言語 / TUI フレームワークの比較

体験モックで求めた見た目（pin ヘッダ・種別ピル・二ペイン・タブ・タイムラインレール・スクロール・キーバインド）を fzf では出しきれない。専用 TUI が要る。

| 候補 | 長所 | 短所 | 判定 |
| --- | --- | --- | --- |
| **Go + Bubble Tea** | gh-dash と同一。単一バイナリ。`lipgloss` で Primer を容易に再現。`viewport`/`list`/`help` 完備。`tea.ExecProcess` で外部 reviewer を綺麗に起動。GitHub-TUI の参照コード多数（gh-dash, glow, soft-serve）。バイナリ起動が ms 級 → hook から叩く CLI にも最適 | Go の冗長さ | ◎ 採用 |
| Rust + Ratatui | 高速・単一バイナリ | suspend/resume と外部起動が手数多い。GitHub-TUI の参照が薄い。オーバースペック | ○ 次点 |
| Python + Textual | CSS ライクで Primer 再現が速い。反復が速い | 単一バイナリでない（uv/pipx 配布）。hook から叩く CLI の起動遅延（~100-300ms） | △ |
| Node + Ink | React 的 | ランタイム依存。gh-dash 的な作り込みに不向き。dotfiles の非 JS 文化と不一致 | △ |
| fzf / bash 継続 | ゼロ依存 | レイアウト忠実度が頭打ち（モックで確認済み） | × |

決め手: **gh-dash 参照**・**単一バイナリ配布**・**外部 reviewer 起動の綺麗さ**・**hook-CLI の起動速度**。この4点すべてで Go+Bubble Tea が最良。

## アーキテクチャ（層の分離）

```
[ 物語レイヤ (本体) ]                     Go 1.22+
  .live-pr/<branch-slug>/
    timeline.jsonl   append-only  {ts,kind,title,body,sha?,...}
    conclusion.md    overwrite    head（現状結論）
    config.toml?     per-repo override

[ 供給: agent hooks ]
  Claude Code settings.json
    Stop        → live-pr hook stop        # セッション要約を1件 append
    PostToolUse → (任意) commit 検出で commit event
  手動          → live-pr pivot "…" / live-pr note "…"
  要約生成       → claude -p headless（初手）。将来 API / 他エージェント差し替え

[ 表示: TUI (Bubble Tea) ]
  live-pr        # 左: PR Conversation timeline / 右: 選択 commit の git show
  reviewer 起動  # Enter → tea.ExecProcess で nvim 等を全画面起動→復帰

[ 出力: PR export ]
  live-pr pr     # conclusion(top) + Development timeline → gh pr create/edit
```

CLI 一覧（Cobra）:
`live-pr`（TUI）/ `live-pr hook stop` / `live-pr append|decision|pivot|note` / `live-pr sync` / `live-pr pr` / `live-pr init`

## 依存ライブラリ（初期）

- `github.com/charmbracelet/bubbletea` — TUI ランタイム
- `github.com/charmbracelet/lipgloss` — スタイル（Primer 配色・ピル・枠）
- `github.com/charmbracelet/bubbles` — viewport / list / help / key
- `github.com/spf13/cobra` — サブコマンド
- `github.com/BurntSushi/toml` — 設定
- 標準 `encoding/json` — JSONL
- `git` / `gh` は exec（ライブラリ不要）

## 実装で確定した事項

1. **要約トリガー**: Claude Codeの`Stop` hookでセッション終端に要約。短時間の重複実行は設定可能な間隔で抑制し、pivotは`live-pr pivot`で手動記録。
2. **timelineの扱い**: `.live-pr/`はgitignoreし、ローカルruntimeとして保持。PR export時に読み込む。
3. **要約手段**: `claude -p` headlessを使用。モデルはTOML設定で上書き可能。
4. **設定形式**: TOML。グローバル設定をper-repo設定で上書き。
5. **TUI**: GitHub PRのConversationを左ペイン、選択commitのdiffを右ペインに表示。外部reviewerはEnterで起動。

## 現在地

P0〜P5とConversation中心のTUIを実装済み。次はgoreleaser / Homebrew tap、shell completion、テーマ設定、他エージェントadapterなどの配布・連携整備。
