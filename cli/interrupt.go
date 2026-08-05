package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/term"
)

// StartESCListener listens for ESC key (ASCII 27 / 0x1B) or SIGINT signal in background during LLM generation.
// It uses non-blocking read deadlines to guarantee term.Restore completes instantly when doneChan closes.
func StartESCListener(cancel context.CancelFunc, doneChan chan struct{}) {
	fd := int(os.Stdin.Fd())

	// 1. Setup Signal handler for SIGINT / SIGTERM as fallback
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		select {
		case <-sigChan:
			cancel()
		case <-doneChan:
			signal.Stop(sigChan)
		}
	}()

	// 2. Setup ESC key stdin listener if terminal is a TTY
	if !term.IsTerminal(fd) {
		return
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return
	}

	go func() {
		defer func() {
			os.Stdin.SetReadDeadline(time.Time{}) // Clear read deadline
			term.Restore(fd, oldState)            // Always restore terminal mode for readline
		}()

		buf := make([]byte, 3)
		for {
			select {
			case <-doneChan:
				return
			default:
				// Use 50ms read deadline so os.Stdin.Read never blocks permanently
				os.Stdin.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
				n, readErr := os.Stdin.Read(buf)

				// If generation finished while reading, exit immediately
				select {
				case <-doneChan:
					return
				default:
				}

				if readErr == nil && n > 0 {
					// ASCII 27 = ESC key, ASCII 3 = Ctrl+C
					if buf[0] == 27 || buf[0] == 3 {
						cancel()
						return
					}
				}
			}
		}
	}()
}
