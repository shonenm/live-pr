package main

import (
	"fmt"
	"os"

	"github.com/shonenm/live-pr/v2/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "live-pr:", err)
		os.Exit(1)
	}
}
