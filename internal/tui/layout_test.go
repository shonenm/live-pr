package tui

import "testing"

func TestListScreenSplitFollowsListRatio(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.w, m.h = 120, 40
	// A narrow reviewer setting must not squeeze the PR list: the list
	// screen splits by list.split_ratio, not diff.split_ratio.
	m.diffSplitRatio = 20
	m.listSplitRatio = 50
	m.layout()
	if m.list.Width() <= m.detail.Width()-4 || m.list.Width() >= m.detail.Width()+4 {
		t.Fatalf("50%% split = list %d / preview %d", m.list.Width(), m.detail.Width())
	}

	m.listSplitRatio = 0 // unset falls back to the built-in list ratio
	m.layout()
	want := 120 * prListPaneRatio / 100
	if got := m.list.Width() + paneChromeW; got != want {
		t.Fatalf("fallback split = %d, want %d", got, want)
	}
}
