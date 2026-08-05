package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// StartInterruptListener listens for SIGINT (Ctrl+C) / SIGTERM in background during LLM generation.
// It uses clean OS signal notification without mutating terminal termios/raw mode, preserving readline state 100%.
func StartInterruptListener(cancel context.CancelFunc, doneChan chan struct{}) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		select {
		case <-sigChan:
			cancel()
		case <-doneChan:
		}
		signal.Stop(sigChan)
	}()
}
