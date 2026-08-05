package cli

import (
	"context"
	"os"

	"golang.org/x/term"
)

// StartESCListener listens for ESC key (ASCII 27 / \x1b) in background during active LLM generation
func StartESCListener(cancel context.CancelFunc, doneChan chan struct{}) {
	fd := int(os.Stdin.Fd())

	// Save terminal state and set to raw mode for single keypress capture
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return
	}

	go func() {
		defer term.Restore(fd, oldState)

		buf := make([]byte, 3)
		for {
			select {
			case <-doneChan:
				return
			default:
				n, readErr := os.Stdin.Read(buf)
				if readErr != nil {
					return
				}
				if n > 0 {
					// Check for ESC key (ASCII 27 / 0x1B) or Ctrl+C (ASCII 3)
					if buf[0] == 27 || buf[0] == 3 {
						cancel()
						return
					}
				}
			}
		}
	}()
}
