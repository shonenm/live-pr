package embeddedterm

import (
	"bytes"
	"encoding/base64"
	"strings"
)

// osc52Prefix opens a clipboard-write sequence: ESC ] 52 ; <Pc> ; <Pd> terminator.
const osc52Prefix = "\x1b]52;"

// maxOSC52 caps a pending sequence so a stream that never terminates one cannot
// grow the buffer without bound.
const maxOSC52 = 1 << 20

// osc52Scanner extracts clipboard payloads from a PTY byte stream. The embedded
// terminal emulator only interprets OSC 7, so clipboard writes from the child
// (Neovim's OSC 52 provider, for example) would otherwise be dropped. Sequences
// may straddle reads, so partial input is carried over between calls.
type osc52Scanner struct {
	pending []byte
}

// scan returns the clipboard payloads completed by data, in order.
func (s *osc52Scanner) scan(data []byte) []string {
	s.pending = append(s.pending, data...)
	var found []string
	for {
		start := bytes.Index(s.pending, []byte(osc52Prefix))
		if start < 0 {
			// Keep only enough tail to match a prefix split across reads.
			if tail := len(osc52Prefix) - 1; len(s.pending) > tail {
				s.pending = append(s.pending[:0], s.pending[len(s.pending)-tail:]...)
			}
			return found
		}
		body := s.pending[start+len(osc52Prefix):]
		end, termLen := oscTerminator(body)
		if end < 0 {
			// Incomplete: keep the sequence for the next read, unless it has
			// grown past anything a real clipboard write would produce.
			s.pending = append(s.pending[:0], s.pending[start:]...)
			if len(s.pending) > maxOSC52 {
				s.pending = nil
			}
			return found
		}
		if text, ok := decodeOSC52(string(body[:end])); ok {
			found = append(found, text)
		}
		s.pending = append(s.pending[:0], body[end+termLen:]...)
	}
}

// oscTerminator locates BEL or ST, returning its offset and length.
func oscTerminator(b []byte) (offset, length int) {
	bel := bytes.IndexByte(b, 0x07)
	st := bytes.Index(b, []byte{0x1b, '\\'})
	switch {
	case bel >= 0 && (st < 0 || bel < st):
		return bel, 1
	case st >= 0:
		return st, 2
	default:
		return -1, 0
	}
}

// decodeOSC52 parses "<Pc>;<Pd>". A "?" payload is a read request, which has no
// text to copy.
func decodeOSC52(body string) (string, bool) {
	_, data, ok := strings.Cut(body, ";")
	if !ok || data == "?" {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", false
	}
	return string(decoded), true
}
