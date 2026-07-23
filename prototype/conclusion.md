CodeDiff レビューモード + codereview コマンド

2コミット間の diff を専用「レビューモード」で開く。サイドバーで c によるレビュー済み印
(○/✓)、ヘルプに reviewed X/Y、,/.=ファイル・[/]=hunk・{}=未チェックへ移動。
:CodeReview / :CodeReviewBranch と zsh 関数 codereview / codereview-branch (cr/crb)。
通常経路 (status/単一rev/WORKING) は判定ゲートで一切変更しない。

status: PR #309 open (base: main) · 2 commits · headless E2E green
