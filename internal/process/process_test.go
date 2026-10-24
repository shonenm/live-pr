package process

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestProcessHelper(t *testing.T) {
	if os.Getenv("LIVE_PR_PROCESS_HELPER") != "1" {
		return
	}
	fmt.Println("ready")
	time.Sleep(time.Minute)
}

func TestCommandContextCancelsRunningProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := CommandContext(ctx, os.Args[0], "-test.run=^TestProcessHelper$")
	cmd.Env = append(os.Environ(), "LIVE_PR_PROCESS_HELPER=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan bool, 1)
	go func() { ready <- bufio.NewScanner(out).Scan() }()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("helper did not start")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper startup timed out")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("sleeping process completed without cancellation")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("process did not stop")
	}
}

func TestCommandContextDoesNotStartAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cmd := CommandContext(ctx, os.Args[0], "-test.run=^TestProcessHelper$")
	if err := cmd.Run(); !errors.Is(err, context.Canceled) || cmd.Process != nil {
		t.Fatalf("started after cancel: %v %+v", err, cmd.Process)
	}
}
