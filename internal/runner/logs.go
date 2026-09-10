package runner

import (
	"context"
	"fmt"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/alternayte/sluice/internal/platform/masking"
	"github.com/alternayte/sluice/internal/runnerapi"
)

// LogShipper batches log lines and sends them with increasing seq (REQ-RUN-002).
// While the API is unreachable it keeps up to MaxPending bytes and drops the oldest
// lines beyond that, then sends a marker with the dropped count (REQ-RUN-006).
type LogShipper struct {
	Send       func(ctx context.Context, b runnerapi.LogBatch) error
	Masker     *masking.Masker
	MaxPending int
	Interval   time.Duration
	BatchBytes int

	mu      sync.Mutex
	pending []runnerapi.LogLine
	bytes   int
	dropped int
	seq     int
	wake    chan struct{}
	done    chan struct{}
	stopped chan struct{}
	sendErr error
}

func (s *LogShipper) init() {
	if s.MaxPending <= 0 {
		s.MaxPending = runnerapi.PendingBufferBytes
	}
	if s.Interval <= 0 {
		s.Interval = runnerapi.BatchInterval
	}
	if s.BatchBytes <= 0 {
		s.BatchBytes = runnerapi.BatchBytes
	}
	s.wake = make(chan struct{}, 1)
	s.done = make(chan struct{})
	s.stopped = make(chan struct{})
}

// Start begins shipping in the background.
func (s *LogShipper) Start(ctx context.Context) {
	s.init()
	go s.loop(ctx)
}

// truncateLine cuts lines above 16 KiB and appends the marker (REQ-RUN-002).
func truncateLine(text string) string {
	if len(text) <= runnerapi.MaxLineBytes {
		return text
	}
	cut := runnerapi.MaxLineBytes - len(runnerapi.TruncatedMarker)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + runnerapi.TruncatedMarker
}

// Add queues one line. It never blocks on the network.
func (s *LogShipper) Add(stream, text string) {
	line := runnerapi.LogLine{TS: time.Now().UTC(), Stream: stream, Text: truncateLine(s.Masker.String(text))}
	s.mu.Lock()
	s.pending = append(s.pending, line)
	s.bytes += len(line.Text) + 48
	for s.bytes > s.MaxPending && len(s.pending) > 1 {
		s.bytes -= len(s.pending[0].Text) + 48
		s.pending = s.pending[1:]
		s.dropped++
	}
	big := s.bytes >= s.BatchBytes
	s.mu.Unlock()
	if big {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// take removes up to BatchBytes of lines, with a drop marker first when lines were dropped.
func (s *LogShipper) take() []runnerapi.LogLine {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []runnerapi.LogLine
	if s.dropped > 0 {
		out = append(out, runnerapi.LogLine{TS: time.Now().UTC(), Stream: "system",
			Text: fmt.Sprintf("[sluice] dropped %d log lines while the API was unreachable", s.dropped)})
		s.dropped = 0
	}
	n := 0
	i := 0
	for i < len(s.pending) && (n == 0 || n+len(s.pending[i].Text) <= s.BatchBytes) {
		n += len(s.pending[i].Text) + 48
		i++
	}
	out = append(out, s.pending[:i]...)
	s.pending = s.pending[i:]
	s.bytes -= n
	return out
}

func (s *LogShipper) loop(ctx context.Context) {
	defer close(s.stopped)
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		closing := false
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			closing = true
		case <-t.C:
		case <-s.wake:
		}
		for {
			batch := s.take()
			if len(batch) == 0 {
				break
			}
			s.seq++
			if err := s.Send(ctx, runnerapi.LogBatch{Seq: s.seq, Lines: batch}); err != nil {
				s.mu.Lock()
				s.sendErr = err
				s.mu.Unlock()
			}
			if !closing {
				s.mu.Lock()
				more := s.bytes >= s.BatchBytes
				s.mu.Unlock()
				if !more {
					break
				}
			}
		}
		if closing {
			return
		}
	}
}

// Close flushes all pending lines and stops the shipper.
func (s *LogShipper) Close() error {
	close(s.done)
	<-s.stopped
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sendErr
}
