// Package debugtime provides opt-in wall-clock timings without a metrics subsystem.
package debugtime

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	enabled = os.Getenv("LIVE_PR_DEBUG_TIMING") != ""
	output  sync.Mutex
)

// Start returns a completion logger when LIVE_PR_DEBUG_TIMING is enabled.
func Start(name string) func() {
	if !enabled {
		return nil
	}
	started := time.Now()
	return func() {
		output.Lock()
		defer output.Unlock()
		fmt.Fprintf(os.Stderr, "live-pr timing: %s %s\n", name, time.Since(started).Round(time.Microsecond))
	}
}
