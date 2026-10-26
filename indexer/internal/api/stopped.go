package api

import "sync"

// Stopped is set by the indexing loop when it gives up for good, and read by the
// status handler. Without it the two conditions the loop itself calls STOPPING —
// a chain reset and a governance change to the epoch length — leave a process that
// is alive, answering 200, and frozen. Under `restart: unless-stopped` Docker will
// not restart something that did not exit, so the container stays green for ever.
type Stopped struct {
	mu     sync.Mutex
	reason string
}

func (s *Stopped) Set(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reason = reason
}

func (s *Stopped) Reason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}
