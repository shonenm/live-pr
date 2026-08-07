package diffview

import (
	"strings"
	"testing"
	"time"
)

func TestRender(t *testing.T) {
	out, err := render("sed 's/foo/bar/g'", "foo\n", 80, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if out != "bar" {
		t.Fatalf("out = %q", out)
	}
}

func TestRenderProvidesPaneWidth(t *testing.T) {
	out, err := render(`printf '%s/%s' "$COLUMNS" "$LIVE_PR_DIFF_WIDTH"`, "", 72, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if out != "72/72" {
		t.Fatalf("out = %q", out)
	}
}

func TestRenderFailureAndTimeout(t *testing.T) {
	if _, err := render("printf 'broken\\nextra' >&2; exit 2", "diff", 80, time.Second); err == nil || !strings.Contains(err.Error(), "broken") || strings.Contains(err.Error(), "extra") {
		t.Fatalf("expected one-line stderr, got %v", err)
	}
	start := time.Now()
	if _, err := render("sleep 1 | cat", "diff", 80, 20*time.Millisecond); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("pipeline outlived timeout: %v", elapsed)
	}
}

func TestBoundedBufferDiscardsExcess(t *testing.T) {
	b := &boundedBuffer{limit: 4}
	if n, err := b.Write([]byte("123456")); err != nil || n != 6 || b.String() != "1234" {
		t.Fatalf("bounded write: n=%d err=%v value=%q", n, err, b.String())
	}
}

func TestRenderTruncatesOutput(t *testing.T) {
	out, err := render("awk 'BEGIN { for (i=0; i<805; i++) print i }'", "", 80, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(out, "\n") + 1; lines != 801 || !strings.HasSuffix(out, "… (truncated)") {
		t.Fatalf("unexpected bounded output: lines=%d suffix=%q", lines, out[len(out)-20:])
	}
}
