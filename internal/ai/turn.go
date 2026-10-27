package ai

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/ai/aidb"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// Turn timing. A running turn writes its draft and heartbeat on each tick; a turn without a
// heartbeat for turnStale fails (spec assistant-background-turns).
const (
	turnTick       = 250 * time.Millisecond
	turnStale      = 60 * time.Second
	turnStaleCheck = 5 * time.Second
)

// Stop reasons of a turn.
const (
	stopEndTurn   = "end_turn"
	stopPending   = "pending_action"
	stopStepLimit = "step_limit_reached"
	stopStopped   = "stopped"
	stopError     = "error"
)

var (
	errTurnNotRunning = httpx.Errorf(http.StatusConflict, "turn_not_running", "no turn is running in this conversation")
	errTurnNotFailed  = httpx.Errorf(http.StatusConflict, "turn_not_failed", "only a failed turn can be retried")
	errServerStopped  = errors.New("the server stopped during the turn")
	errStopRequested  = errors.New("the user stopped the turn")
)

// turns holds the base context and the running turns of this instance. RunTurns sets the
// base context; without it, turns use a context that only the turn cancels.
type turns struct {
	mu   sync.Mutex
	base context.Context
	wg   sync.WaitGroup
}

func (ts *turns) baseContext() context.Context {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.base == nil {
		return context.Background()
	}
	return ts.base
}

// turn is one running turn. The model writes text deltas into draft; the keeper writes the
// draft and the heartbeat to the conversation and reads a stop request.
type turn struct {
	convID  uuid.UUID
	mu      sync.Mutex
	draft   strings.Builder
	stopped atomic.Bool
	cancel  context.CancelCauseFunc
}

func (t *turn) write(delta string) {
	t.mu.Lock()
	t.draft.WriteString(delta)
	t.mu.Unlock()
}

func (t *turn) text() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.draft.String()
}

func (t *turn) resetDraft() {
	t.mu.Lock()
	t.draft.Reset()
	t.mu.Unlock()
}

// RunTurns gives turns the server lifetime as base context, and fails turns whose
// instance stopped. When ctx ends, it waits for the turns of this instance to end.
func (s *Service) RunTurns(ctx context.Context) {
	s.turns.mu.Lock()
	s.turns.base = ctx
	s.turns.mu.Unlock()
	t := time.NewTicker(turnStaleCheck)
	defer t.Stop()
	for {
		if _, err := aidb.New(s.Pool).FailStaleTurns(ctx, ptr(s.Clock.Now().Add(-turnStale))); err != nil && ctx.Err() == nil {
			s.Log.Warn("fail stale assistant turns", "err", err)
		}
		select {
		case <-ctx.Done():
			s.turns.wg.Wait()
			return
		case <-t.C:
		}
	}
}

func ptr[T any](v T) *T { return &v }

// startTurn marks a conversation of the current user as running. It returns the model and
// the provider before any work starts, so that these errors are JSON answers.
func (s *Service) startTurn(ctx context.Context, convID uuid.UUID, retry bool) (Model, *ProviderConfig, error) {
	m, cfg, err := s.Model(ctx)
	if err != nil {
		return nil, nil, err
	}
	c, err := s.conversation(ctx, convID)
	if err != nil {
		return nil, nil, err
	}
	n, err := aidb.New(s.Pool).StartTurn(ctx, aidb.StartTurnParams{Now: ptr(s.Clock.Now()), ID: convID, UserID: c.UserID, Retry: retry})
	if err != nil {
		return nil, nil, err
	}
	if n == 0 {
		if c, err = s.conversation(ctx, convID); err != nil {
			return nil, nil, err
		}
		if c.TurnStatus == "running" {
			return nil, nil, errTurnRunning
		}
		return nil, nil, errTurnNotFailed
	}
	return m, cfg, nil
}

// failTurn ends a started turn that could not launch.
func (s *Service) failTurn(ctx context.Context, convID uuid.UUID, err error) {
	if e := aidb.New(s.Pool).EndTurn(context.WithoutCancel(ctx), aidb.EndTurnParams{ID: convID, Status: "failed", Stop: stopError,
		Error: err.Error()}); e != nil {
		s.Log.Warn("end assistant turn", "conversation", convID, "err", e)
	}
}

// launch runs body as the turn of a started conversation in a goroutine. The turn keeps the
// values of the request context, for example the principal and the audit actor, but not its
// cancellation: a closed browser does not stop it.
func (s *Service) launch(reqCtx context.Context, convID uuid.UUID, body func(ctx context.Context, t *turn) (string, error)) {
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(reqCtx))
	release := context.AfterFunc(s.turns.baseContext(), func() { cancel(errServerStopped) })
	t := &turn{convID: convID, cancel: cancel}
	s.turns.wg.Add(1)
	go func() {
		defer s.turns.wg.Done()
		defer release()
		defer cancel(nil)
		kept := make(chan struct{})
		keeperCtx, stopKeeper := context.WithCancel(context.WithoutCancel(ctx))
		go func() {
			defer close(kept)
			s.keep(keeperCtx, t)
		}()
		stop, err := s.guard(ctx, t, body)
		stopKeeper()
		<-kept
		s.endTurn(ctx, t, stop, err)
	}()
}

// guard runs body and turns a panic into an error, so that the turn ends and a user can retry.
func (s *Service) guard(ctx context.Context, t *turn, body func(ctx context.Context, t *turn) (string, error)) (stop string, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.Log.Error("assistant turn stopped with a panic", "conversation", t.convID, "panic", r, "stack", string(debug.Stack()))
			stop, err = stopError, errors.New("the turn stopped with an internal error")
		}
	}()
	return body(ctx, t)
}

// keep writes the draft and the heartbeat on each tick and cancels the turn on a stop request.
func (s *Service) keep(ctx context.Context, t *turn) {
	tick := time.NewTicker(turnTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		stop, err := aidb.New(s.Pool).TickTurn(ctx, aidb.TickTurnParams{DraftText: t.text(), Now: ptr(s.Clock.Now()), ID: t.convID})
		if err != nil {
			if ctx.Err() == nil {
				s.Log.Warn("assistant turn heartbeat", "conversation", t.convID, "err", err)
			}
			continue
		}
		if stop && !t.stopped.Swap(true) {
			t.cancel(errStopRequested)
		}
	}
}

// endTurn saves the state of an ended turn. A stopped turn keeps its streamed text as the
// answer, marked as stopped.
func (s *Service) endTurn(ctx context.Context, t *turn, stop string, err error) {
	cause := context.Cause(ctx)
	ctx = context.WithoutCancel(ctx)
	status, msg := "idle", ""
	switch {
	case t.stopped.Load():
		stop = stopStopped
		if e := s.storeMessage(ctx, t.convID, "assistant", []Block{{Type: BlockText, Text: t.text(), Stopped: true}}); e != nil {
			status, stop, msg = "failed", stopError, "the stopped answer cannot be saved"
		}
	case err != nil:
		status, stop, msg = "failed", stopError, err.Error()
		if errors.Is(cause, errServerStopped) {
			msg = errServerStopped.Error()
		}
	}
	if e := aidb.New(s.Pool).EndTurn(ctx, aidb.EndTurnParams{ID: t.convID, Status: status, Stop: stop, Error: msg}); e != nil {
		s.Log.Warn("end assistant turn", "conversation", t.convID, "err", e)
	}
}

// StopTurn asks the running turn of a conversation to stop. The instance that runs it
// reads the request on its next tick.
func (s *Service) StopTurn(ctx context.Context, convID uuid.UUID) error {
	c, err := s.conversation(ctx, convID)
	if err != nil {
		return err
	}
	n, err := aidb.New(s.Pool).RequestStop(ctx, aidb.RequestStopParams{ID: convID, UserID: c.UserID})
	if err == nil && n == 0 {
		return errTurnNotRunning
	}
	return err
}

// RetryTurn runs a failed turn again from the saved messages. It adds no user message.
func (s *Service) RetryTurn(ctx context.Context, convID uuid.UUID) error {
	m, cfg, err := s.startTurn(ctx, convID, true)
	if err != nil {
		return err
	}
	s.launch(ctx, convID, func(ctx context.Context, t *turn) (string, error) {
		return s.runTurn(ctx, t, m, cfg)
	})
	return nil
}

// SetAutoAccept turns accept-all of a conversation on or off.
func (s *Service) SetAutoAccept(ctx context.Context, convID uuid.UUID, on bool) (Conversation, error) {
	c, err := s.conversation(ctx, convID)
	if err != nil {
		return Conversation{}, err
	}
	c, err = aidb.New(s.Pool).SetAutoAccept(ctx, aidb.SetAutoAcceptParams{ID: convID, UserID: c.UserID, AutoAccept: on})
	return conversationOut(c), err
}

// StreamTurn sends the conversation each time it changes while a turn runs, then "end". It
// reads Postgres, so it serves a turn of any instance (spec assistant-background-turns).
func (s *Service) StreamTurn(w http.ResponseWriter, r *http.Request, convID uuid.UUID) {
	ctx := r.Context()
	if _, err := s.conversation(ctx, convID); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	tick := time.NewTicker(turnTick)
	defer tick.Stop()
	var last [32]byte
	idle := 0
	for {
		d, err := s.ConversationDetail(ctx, convID)
		if err != nil {
			return // the conversation is gone or the client left
		}
		b, _ := json.Marshal(d)
		if h := sha256.Sum256(b); h != last {
			last = h
			if _, err := fmt.Fprintf(w, "event: conversation\ndata: %s\n\n", b); err != nil {
				return
			}
			_ = rc.Flush()
			idle = 0
		} else if idle++; idle%60 == 0 {
			_, _ = fmt.Fprint(w, ": keep-alive\n\n")
			_ = rc.Flush()
		}
		if d.TurnStatus != "running" {
			_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
			_ = rc.Flush()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
