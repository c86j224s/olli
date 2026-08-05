package cli

import (
	"fmt"
	"sync"
	"time"
)

type Spinner struct {
	stopChan chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	running  bool
}

func NewSpinner() *Spinner {
	return &Spinner{
		stopChan: make(chan struct{}),
	}
}

// Start displays a subtle animated CLI loading spinner while waiting for LLM response
func (s *Spinner) Start(message string) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopChan = make(chan struct{})
	s.mu.Unlock()

	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	s.wg.Add(1)

	go func() {
		defer s.wg.Done()
		idx := 0
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-s.stopChan:
				// Clear the line when stopped
				fmt.Print("\r\033[K")
				return
			case <-ticker.C:
				frame := frames[idx%len(frames)]
				fmt.Printf("\r%s%s%s %s%s%s", ColorYellow, frame, ColorReset, ColorGray, message, ColorReset)
				idx++
			}
		}
	}()
}

// Stop erases the spinner line from terminal cleanly
func (s *Spinner) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	close(s.stopChan)
	s.wg.Wait()
}
