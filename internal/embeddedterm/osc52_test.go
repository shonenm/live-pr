package embeddedterm

import (
	"encoding/base64"
	"reflect"
	"testing"
)

func osc52BEL(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
}

func TestOSC52ScannerExtractsClipboardWrites(t *testing.T) {
	for name, test := range map[string]struct {
		chunks []string
		want   []string
	}{
		"bel terminator": {
			chunks: []string{"before" + osc52BEL("yanked") + "after"},
			want:   []string{"yanked"},
		},
		"st terminator": {
			chunks: []string{"\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("via ST")) + "\x1b\\"},
			want:   []string{"via ST"},
		},
		"two writes in one read": {
			chunks: []string{osc52BEL("first") + osc52BEL("second")},
			want:   []string{"first", "second"},
		},
		"no clipboard traffic": {
			chunks: []string{"plain output\x1b]7;file:///tmp\x07more"},
			want:   nil,
		},
		"read request is not a copy": {
			chunks: []string{"\x1b]52;c;?\x07"},
			want:   nil,
		},
		"invalid base64 is ignored": {
			chunks: []string{"\x1b]52;c;!!!!\x07"},
			want:   nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var scanner osc52Scanner
			var got []string
			for _, chunk := range test.chunks {
				got = append(got, scanner.scan([]byte(chunk))...)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("scan = %q, want %q", got, test.want)
			}
		})
	}
}

// A 4 KiB PTY read can land anywhere inside the escape, so the scanner must
// carry a partial sequence across calls.
func TestOSC52ScannerHandlesSequenceSplitAcrossReads(t *testing.T) {
	full := "noise" + osc52BEL("split across reads") + "tail"
	for split := 1; split < len(full); split++ {
		var scanner osc52Scanner
		var got []string
		got = append(got, scanner.scan([]byte(full[:split]))...)
		got = append(got, scanner.scan([]byte(full[split:]))...)
		if want := []string{"split across reads"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("split at %d: scan = %q, want %q", split, got, want)
		}
	}
}

func TestOSC52ScannerDropsRunawaySequence(t *testing.T) {
	var scanner osc52Scanner
	scanner.scan([]byte("\x1b]52;c;"))
	scanner.scan(make([]byte, maxOSC52+1)) // never terminated
	if len(scanner.pending) != 0 {
		t.Fatalf("pending grew to %d bytes; want the buffer dropped", len(scanner.pending))
	}
}
