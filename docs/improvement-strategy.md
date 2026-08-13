# コード改善戦略 (2026-08)

コード品質・性能・可読性・テストカバレッジの網羅調査結果と、全45所見を消化するための PR 分割計画。

## 調査概要

- 規模: 16,187行 / 90ファイル / 22パッケージ (Go 1.25)
- 手法: 6スライスを4次元で並列調査 → 敵対的検証 → 統合 (13エージェント)
- 前提健全性: `go vet` clean、TODO/FIXME なし、重大 correctness バグ (high) ゼロ
- 検証通過: 45所見 (却下1)。内訳 品質15 / テスト15 / 性能8 / 可読性7、重大度 medium 13 / low 32
- 生データ: `scratchpad/audit_confirmed.json`

### ホットスポット

- `internal/tui` が 5,486行と突出 (model.go 1542 / pr_list.go 1179)。所見も集中。
- カバレッジの穴: cmd 31% / store 36% / prtemplate 33% / richcontent 46% / embeddedterm 53% / git 54%。ロジックは書けているが CLI・IO 境界の検証が薄い。

## PR 分割方針

- 1 PR = 1 関心事。レビュー可能な差分に保つ。
- correctness / perf / readability / test を混ぜない (レビュー観点が変わるため)。ただし同一パッケージの小修正は束ねる。
- 大きく risky な god-struct 分割 (P13) は最後。先行 PR のマージ後に着手し衝突を避ける。
- 依存があるものだけ順序固定。それ以外は並列でよい。

### PR 一覧

| PR | 主題 | 次元 | 規模 | 依存 |
|----|------|------|------|------|
| P1 | cmd 入力検証・出力規約 | 品質 | S | なし |
| P2 | cmd テスト補強 | テスト | M | なし |
| P3 | git 堅牢化 | 品質 | M | なし |
| P4 | git テスト補強 | テスト | M | P3 推奨 |
| P5 | publish 修正・整理 | 品質/可読/性能 | M | なし |
| P6 | store 整理・テスト | 可読/テスト | S | なし |
| P7 | 補助パッケージ小修正 | 混在 | S | なし |
| P8 | レンダリング性能 | 性能/テスト | S | なし |
| P9 | github API 形状 | 可読/品質 | S | なし |
| P10 | tui 正当性・デッドコード | 品質 | S | なし |
| P11 | tui 性能 | 性能 | M | なし |
| P12 | tui テスト補強 | テスト | S | なし |
| P13 | tui 可読性リファクタ | 可読 | L | P10-P12 後 |

推奨着手順: P1・P10・P8 (即効・低リスク) → P3/P5/P6/P7/P9 → P2/P4/P11/P12 → P13。

---

## 各 PR 詳細

### P1: cmd 入力検証・出力規約 (品質 / S)

- `cmd/review.go:113` — `fmt.Sscan` が末尾ゴミを黙認 (`"5x"`→5、error なし)。誤インデックス削除の温床。`strconv.Atoi`＋範囲チェックに置換。
- `cmd/sync.go:28` — `fmt.Printf` で直接 stdout。他コマンドの `cmd.OutOrStdout()` 規約を破りテスト不能。`Fprintf(cmd.OutOrStdout(), ...)` へ。
- `cmd/pr.go:68` — base/draft/force-managed-body フラグを prCmd と2サブコマンドで三重登録。`prCmd.PersistentFlags()` に一度登録し継承させ重複削除。

### P2: cmd テスト補強 (テスト / M)

- `cmd/hook.go:24` — Stop hook エントリが 0%。非repo/repo で never-block 契約を固定。
- `cmd/review.go:80` — review write 系 (add/body/delete/submit) の RunE 未テスト。保存後の ReviewDraft と異常系 (invalid side / empty body) を検証。
- `cmd/comment.go:133` — `commentEditCmd` (最も分岐が多い) が未テスト。commit-edit 拒否・no-field-changed・kind+body 編集の永続化。
- `cmd/status.go:48` — `loadStatus` の refresh 経路 (fetch / ErrPRNotFound clear / cache save) 未テスト。demo mock gh を利用。
- `cmd/comment.go:43` — `readTextFlag` の conflict 分岐と file-read 分岐が未テスト。

### P3: git 堅牢化 (品質 / M)

- `internal/git/git.go:452` — `FetchPull` のネットワーク fetch に context/timeout 無し。git ハングで TUI 無限ブロック。`exec.CommandContext`＋30s (New() と同様)。
- `internal/git/git.go:105` — リビジョン範囲に `--end-of-options` 無し。`-` 始まり ref を git がオプション解釈。log/rev-list/merge-base/merge-tree に一貫付与。
- `internal/git/git.go:234` — `contentConflicts` が片側のみ存在するファイルを黙殺し modify/delete 衝突を報告しない。片側欠落を衝突扱いにするか、意図を明記。
- `internal/git/git.go:421` — `Show`/`ShowStat`/`FileDiffRange` が全 git エラーを空文字に握り潰し、失敗と空 diff が区別不能。`(string, error)` 化。

### P4: git テスト補強 (テスト / M、P3 推奨)

- `internal/git/git.go:201` — `contentConflicts` fallback (temp file＋`git merge-file`) が 0%。merge-tree が衝突を報告しないが内容衝突するケースを強制。
- `internal/git/git.go:55` — base 解決 (`DefaultBase` 0% / `ResolveBase` origin-tracking 分岐) がほぼ未テスト。origin/<base> 優先と main/master fallback 順を検証。

### P5: publish 修正・整理 (品質/可読/性能 / M)

- `internal/publish/publish.go:145` — Create 後の FindOpen 失敗時に Number=0 の偽PRをキャッシュ。URL から番号を拾うか上書きしない。テスト追加。
- `internal/publish/publish.go:103` — `BuildPreview` が store 再探索・commits/timeline 再列挙で重複IO。解決済み `*store.Store` を渡す。
- `internal/publish/publish.go:81` — `run()` が base解決・衝突検査・remote create/update・cache書き込みを80行に混在 (変数shadow有)。base解決と remote step をヘルパ抽出。

### P6: store 整理・テスト (可読/テスト / S)

- `internal/store/store.go:95` — `WriteConclusion` の rename が歪な if/else (成功が else の `return err` を通る)。`else after return` を解消しフラット化。
- `internal/store/store.go:77` — `WriteConclusion` (conclusion.md 唯一の writer) が 0%。atomic-rename と Windows fallback を含めテスト。
- `internal/store/store.go:176` — `stateRoot` の GOOS 分岐 18%。XDG_STATE_HOME 未設定時の per-GOOS 経路と `HasData` を検証。

### P7: 補助パッケージ小修正 (混在 / S)

- `internal/event/event.go:130` — `Load` がスキャン行を不要コピー。`sc.Bytes()` で1確保削減。
- `internal/event/event.go:153` — `Load` の update-after-delete 無視分岐、`Delete` の error/duplicate 経路が未テスト。
- `internal/hook/hook.go:68` — `recentlySummarized` が timestamp パース失敗時に「最近でない」を返しスロットル無効化 (重複サマリ)。保守的に true 側へ。
- `internal/prbody/prbody.go:51` — 先頭が裸の `#` だと `Title` が空文字。空行スキップを追加。
- `internal/prtemplate/template.go:38` — `Seed` の4分岐が全て未テスト (パッケージ33%)。

### P8: レンダリング性能 (性能/テスト / S)

- `internal/markdown/render.go:27` — キャッシュキーを毎回 `fmt.Sprintf("%d\x00%s", w, text)` で本文コピー生成 (ヒット時も)。`struct{w int; s string}` キーへ。
- `internal/markdown/render.go:49` — キャッシュ溢れ時に `clear()` で全512件破棄→次フレームで可視カード全再描画のヒッチ。1件退避に。
- `internal/richcontent/render.go:141` — アバター平均色算出が最大1600万回の `img.At().RGBA()`。ストライドサンプリング (数千px上限) で見た目同一・境界化。
- `internal/richcontent/render.go:96` — `AvatarColorContext` の URL 許可リスト (セキュリティ関連) 拒否分岐が未検証。http:// と非GitHub https を検証。

### P9: github API 形状 (可読/品質 / S)

- `internal/github/github.go:513` — `IssueDetail` が4値2末尾エラー返し (comments, activities, error, error) で誤配線しやすい。struct 返しへ。
- `internal/github/github.go:221` — cwd キーの global `sync.Map` が無期限キャッシュ・エビクション無し。単一リポ TUI なら Client に載せる。

### P10: tui 正当性・デッドコード (品質 / S)

- `internal/tui/update_keys.go:153` — PR一覧の j/k が `moveCursorBy` を経由せず、最終行到達で次ページ fetch が発火しない (G/ctrl+d のみ)。`moveCursorBy(±1)` に統一。
- `internal/tui/model.go:679` — `Init()` が値レシーバで `loadSpinner`/`spinnerRunning` への代入を破棄 (New() 依存のデッドガード)。削除。
- `internal/tui/local_edit.go:184` — review-submit-then-type 導入で到達不能になった分岐。到達すれば Approve/RequestChanges を Comment に降格。削除。
- `internal/tui/update_keys.go:147` — PreviewUp/Down が70行目で return 済みなのに switch に重複 case (到達不能)。削除。
- `internal/tui/model.go:100` — `c`/`q`/`l` のキー二重割当を `SetEnabled` のみで曖昧解消。現状は正しく動く。両画面の各キー動作を固定するテストを追加。

### P11: tui 性能 (性能 / M)

- `internal/tui/conversation.go:150` — 会話カードを毎キーストロークで全再描画 (markdown＋lipgloss)。行キャッシュ無し。`(item.key, selected, width)` でメモ化。体感で最も効く。
- `internal/tui/update_messages.go:179` — preview 1件ロード毎に O(pages×prs) スキャン＋全 `applyPRFilters`＋navigator キャッシュ同期ディスク書き込み。番号→位置索引化＋書き込みデバウンス。
- `internal/tui/github_review.go:104` — `handleReviewSubmitKey` が typing 中に未使用の events スライスを毎キー確保。typing 分岐の外へ移動。
- `internal/tui/pr_list.go:520` — `matchesPRFilter` がフィルタ haystack をトークンループ内で再構築。ループ外で一度構築。

### P12: tui テスト補強 (テスト / S)

- `internal/tui/conversation.go:280` — `activitySummary` の assigned/review_requested/renamed/force-pushed 分岐が未テスト。table test。
- `internal/tui/local_edit.go:82` — `parseLocalComment` の異常系 (prefix欠落/不正kind/空title) と `allowSummary` ゲートが未テスト。
- `internal/tui/tui_test.go:909` — j/k 最終行ページングとクランプが未テスト (moveCursorTo のみ)。

### P13: tui 可読性リファクタ (可読 / L、P10-P12 後)

- `internal/tui/model.go:290` — `Model` が約90フィールドの god struct。filter/localEdit/reviewSubmit/diff 等のサブ struct へ分割。
- `internal/tui/pr_list.go:1035` — `renderPRRow` の選択/非選択で同一 meta ライン二重構築 (手動同期)。fragment を一度組み style wrapper で背景付与。
- `internal/tui/pr_list.go:263` — `applyPRFilters` 約90行、view count を3回計算・state 導出をインライン再実装。`standardPRListState` 再利用＋ヘルパ分割。

---

## 全所見インデックス (次元別)

各項目は上記 PR に割当済み。ファイル:行 → PR。

品質 (15): review.go:113→P1, sync.go:28→P1, git.go:452→P3, git.go:105→P3, git.go:234→P3, git.go:421→P3, publish.go:145→P5, github.go:221→P9, hook.go:68→P7, prbody.go:51→P7, markdown/render.go:49→P8, update_keys.go:153→P10, model.go:679→P10, local_edit.go:184→P10, model.go:100→P10

性能 (8): markdown/render.go:27→P8, richcontent/render.go:141→P8, event.go:130→P7, publish.go:103→P5, conversation.go:150→P11, update_messages.go:179→P11, github_review.go:104→P11, pr_list.go:520→P11

可読性 (7): github.go:513→P9, publish.go:81→P5, store.go:95→P6, pr.go:68→P1, model.go:290→P13, pr_list.go:1035→P13, pr_list.go:263→P13

テスト (15): hook.go:24→P2, review.go:80→P2, comment.go:133→P2, status.go:48→P2, comment.go:43→P2, git.go:201→P4, git.go:55→P4, store.go:77→P6, store.go:176→P6, event.go:153→P7, prtemplate/template.go:38→P7, richcontent/render.go:96→P8, conversation.go:280→P12, local_edit.go:82→P12, tui_test.go:909→P12
