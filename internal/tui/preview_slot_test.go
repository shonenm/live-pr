package tui

import (
	"errors"
	"testing"
)

// A row held back by the in-flight limit must get its preview once a slot
// frees, even when the fetch that freed it failed.
func TestFailedPRPreviewFreesASlotForTheSelectedRow(t *testing.T) {
	m := previewListModel()
	m.prList.previewLoading = map[int]bool{7: true, 8: true, 9: true}
	m, cmd := m.handlePRPreviewLoaded(prPreviewLoaded{generation: m.prList.generation, number: 7, err: errors.New("network unavailable")})
	if cmd == nil || m.prList.previewDue != 1 {
		t.Fatalf("the selected row was not re-armed: cmd=%v due=%d", cmd, m.prList.previewDue)
	}
}
