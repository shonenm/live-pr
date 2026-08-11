package summarize

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantTitle string
		wantBody  string
	}{
		{name: "summary", input: "\n# Keep cache-first startup\n\n- Added lazy previews\n- Preserved stale-result guards\n", wantTitle: "Keep cache-first startup", wantBody: "- Added lazy previews\n- Preserved stale-result guards"},
		{name: "multibyte", input: "## 日本語タイトル\n\n- 本文", wantTitle: "日本語タイトル", wantBody: "- 本文"},
		{name: "empty", input: " \n", wantTitle: "", wantBody: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse(tt.input)
			if got.Title != tt.wantTitle || got.Body != tt.wantBody {
				t.Fatalf("Parse() = %#v, want title=%q body=%q", got, tt.wantTitle, tt.wantBody)
			}
		})
	}
}
