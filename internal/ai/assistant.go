package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/ai/aidb"
	"github.com/alternayte/sluice/internal/audit"
	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// Assistant limits (REQ-AI-005, REQ-AI-006).
const (
	maxSteps            = 20
	maxInvalidProposals = 3
	maxMessageBytes     = 64 << 10
	// uiResultChars limits the tool result text in the event stream. The model gets more.
	uiResultChars = 4000
)

// Conversation is one assistant conversation of the current user.
type Conversation struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
}

// ConversationList lists the conversations of the current user, newest first.
type ConversationList struct {
	Items []Conversation `json:"items"`
}

// StoredMessage is one saved message. Tool messages hold the tool results of one step.
type StoredMessage struct {
	ID        uuid.UUID `json:"id"`
	Role      string    `json:"role" enum:"user,assistant,tool"`
	Content   []Block   `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// PendingAction is one mutating tool call that waits for the user (SI-07).
type PendingAction struct {
	ID         uuid.UUID       `json:"id"`
	ToolCallID string          `json:"tool_call_id"`
	Tool       string          `json:"tool"`
	Arguments  json.RawMessage `json:"arguments"`
	Status     string          `json:"status" enum:"pending,confirmed,rejected"`
	CreatedAt  time.Time       `json:"created_at"`
}

// ConversationDetail is a conversation with its messages and actions.
type ConversationDetail struct {
	Conversation
	Messages []StoredMessage `json:"messages"`
	Actions  []PendingAction `json:"actions"`
}

// MessageIn is the body of a user message.
type MessageIn struct {
	Text string `json:"text" minLength:"1" maxLength:"20000"`
}

var (
	errConversationNotFound = httpx.Errorf(http.StatusNotFound, "conversation_not_found", "conversation not found")
	errActionNotFound       = httpx.Errorf(http.StatusNotFound, "action_not_found", "no pending action with this ID")
	errActionsPending       = httpx.Errorf(http.StatusConflict, "actions_pending", "confirm or reject the pending actions first")
	errTurnRunning          = httpx.Errorf(http.StatusConflict, "turn_running", "the assistant is already answering in this conversation")
)

// turns allows one running turn per conversation on this instance.
var turns sync.Map

func lockTurn(id uuid.UUID) (func(), bool) {
	v, _ := turns.LoadOrStore(id, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, false
	}
	return mu.Unlock, true
}

func userOf(ctx context.Context) (*kernel.Principal, error) {
	p := kernel.FromContext(ctx)
	if p == nil {
		return nil, httpx.Errorf(http.StatusUnauthorized, "unauthenticated", "sign in first")
	}
	return p, nil
}

func (s *Service) conversation(ctx context.Context, id uuid.UUID) (aidb.AiConversation, error) {
	p, err := userOf(ctx)
	if err != nil {
		return aidb.AiConversation{}, err
	}
	c, err := aidb.New(s.Pool).GetConversation(ctx, aidb.GetConversationParams{ID: id, UserID: p.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return c, errConversationNotFound
	}
	return c, err
}

func conversationOut(c aidb.AiConversation) Conversation {
	return Conversation{ID: c.ID, Title: c.Title, CreatedAt: c.CreatedAt}
}

// Conversations lists the conversations of the current user.
func (s *Service) Conversations(ctx context.Context) ([]Conversation, error) {
	p, err := userOf(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := aidb.New(s.Pool).ListConversations(ctx, aidb.ListConversationsParams{UserID: p.UserID, Limit: 100})
	if err != nil {
		return nil, err
	}
	out := make([]Conversation, 0, len(rows))
	for _, r := range rows {
		out = append(out, conversationOut(r))
	}
	return out, nil
}

// CreateConversation starts a conversation of the current user.
func (s *Service) CreateConversation(ctx context.Context, title string) (Conversation, error) {
	p, err := userOf(ctx)
	if err != nil {
		return Conversation{}, err
	}
	id, _ := uuid.NewV7()
	c, err := aidb.New(s.Pool).CreateConversation(ctx, aidb.CreateConversationParams{ID: id, UserID: p.UserID, Title: title, CreatedAt: s.Clock.Now()})
	return conversationOut(c), err
}

// DeleteConversation deletes a conversation of the current user.
func (s *Service) DeleteConversation(ctx context.Context, id uuid.UUID) error {
	p, err := userOf(ctx)
	if err != nil {
		return err
	}
	n, err := aidb.New(s.Pool).DeleteConversation(ctx, aidb.DeleteConversationParams{ID: id, UserID: p.UserID})
	if err == nil && n == 0 {
		return errConversationNotFound
	}
	return err
}

// ConversationDetail returns a conversation with its messages and actions.
func (s *Service) ConversationDetail(ctx context.Context, id uuid.UUID) (ConversationDetail, error) {
	c, err := s.conversation(ctx, id)
	if err != nil {
		return ConversationDetail{}, err
	}
	q := aidb.New(s.Pool)
	rows, err := q.ListMessages(ctx, id)
	if err != nil {
		return ConversationDetail{}, err
	}
	acts, err := q.ListPendingActions(ctx, id)
	if err != nil {
		return ConversationDetail{}, err
	}
	out := ConversationDetail{Conversation: conversationOut(c), Messages: []StoredMessage{}, Actions: []PendingAction{}}
	for _, r := range rows {
		var blocks []Block
		_ = json.Unmarshal(r.Content, &blocks)
		out.Messages = append(out.Messages, StoredMessage{ID: r.ID, Role: r.Role, Content: blocks, CreatedAt: r.CreatedAt})
	}
	for _, a := range acts {
		out.Actions = append(out.Actions, actionOut(a))
	}
	return out, nil
}

func actionOut(a aidb.AiPendingAction) PendingAction {
	return PendingAction{ID: a.ID, ToolCallID: a.ToolCallID, Tool: a.Tool, Arguments: a.Arguments, Status: a.Status, CreatedAt: a.CreatedAt}
}

func (s *Service) storeMessage(ctx context.Context, convID uuid.UUID, role string, blocks []Block) error {
	b, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	id, _ := uuid.NewV7()
	return aidb.New(s.Pool).InsertMessage(context.WithoutCancel(ctx), aidb.InsertMessageParams{ID: id, ConversationID: convID, Role: role, Content: b, CreatedAt: s.Clock.Now()})
}

// emitter writes server-sent events of one turn.
type emitter struct {
	w http.ResponseWriter
	f http.Flusher
}

func startStream(w http.ResponseWriter) *emitter {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	f, _ := w.(http.Flusher)
	e := &emitter{w: w, f: f}
	e.flush()
	return e
}

func (e *emitter) flush() {
	if e.f != nil {
		e.f.Flush()
	}
}

func (e *emitter) send(event string, v any) {
	b, _ := json.Marshal(v)
	_, _ = fmt.Fprintf(e.w, "event: %s\ndata: %s\n\n", event, b)
	e.flush()
}

func (e *emitter) fail(code, message string) {
	e.send("error", map[string]string{"code": code, "message": message})
	e.send("done", map[string]string{"stop": "error"})
}

// assistantTools returns the tools the role of p allows (Appendix B).
func assistantTools(p *kernel.Principal) []Tool {
	var out []Tool
	for _, t := range Tools() {
		if p.Can(t.Role) {
			out = append(out, t)
		}
	}
	return out
}

func assistantSystem(p *kernel.Principal) string {
	return "You are the assistant of Sluice, an orchestrator of flows of tasks. The user is " + p.Email + " with the role " + p.Role.String() + ". " +
		"Use the tools to read namespaces, flows, files, executions, logs and metrics. Do not guess facts that a tool can read. " +
		"Mutating tools run only after the user confirms them. " +
		"To write a flow, call propose_change first, fix all validation issues, then call apply_change with the same files. " +
		"Answer briefly."
}

// history converts saved messages into model messages. Tool results go into user
// messages, and messages of the same role in a row are joined. It also returns the
// assistant steps and the invalid proposals since the last user text.
func history(rows []aidb.AiMessage) ([]Message, int, int) {
	var msgs []Message
	steps, invalid := 0, 0
	for _, r := range rows {
		var blocks []Block
		_ = json.Unmarshal(r.Content, &blocks)
		role := r.Role
		switch r.Role {
		case "user":
			steps, invalid = 0, 0
		case "assistant":
			steps++
		case "tool":
			role = "user"
			for _, b := range blocks {
				if b.ToolName == "propose_change" && strings.Contains(b.Text, `"valid":false`) {
					invalid++
				}
			}
		}
		if n := len(msgs); n > 0 && msgs[n-1].Role == role {
			msgs[n-1].Content = append(msgs[n-1].Content, blocks...)
			continue
		}
		msgs = append(msgs, Message{Role: role, Content: blocks})
	}
	return msgs, steps, invalid
}

func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// runTurn calls the model until it answers without tool calls, a mutating call waits for
// the user, or the step limit is reached (REQ-AI-005).
func (s *Service) runTurn(ctx context.Context, m Model, convID uuid.UUID, em *emitter) {
	p := kernel.FromContext(ctx)
	tools := assistantTools(p)
	defs := make([]ToolDef, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, ToolDef{Name: t.Name, Description: t.Description, InputSchema: t.Schema})
	}
	for {
		rows, err := aidb.New(s.Pool).ListMessages(ctx, convID)
		if err != nil {
			em.fail("internal", "the conversation cannot be read")
			return
		}
		msgs, steps, invalid := history(rows)
		if steps >= maxSteps {
			_ = s.storeMessage(ctx, convID, "assistant", []Block{{Type: BlockText, Text: "I stopped after 20 tool steps (step_limit_reached)."}})
			em.send("error", map[string]string{"code": "step_limit_reached", "message": "the assistant stopped after 20 tool steps"})
			em.send("done", map[string]string{"stop": "step_limit_reached"})
			return
		}
		resp, err := m.Complete(ctx, Request{System: assistantSystem(p), Messages: msgs, Tools: defs}, func(d string) {
			em.send("text", map[string]string{"delta": d})
		})
		if err != nil {
			em.fail("provider_error", err.Error())
			return
		}
		var blocks []Block
		if resp.Text != "" {
			blocks = append(blocks, Block{Type: BlockText, Text: resp.Text})
		}
		for _, c := range resp.ToolCalls {
			blocks = append(blocks, Block{Type: BlockToolUse, ToolUseID: c.ID, ToolName: c.Name, Input: c.Input})
		}
		if len(blocks) == 0 {
			blocks = []Block{{Type: BlockText, Text: ""}}
		}
		if err := s.storeMessage(ctx, convID, "assistant", blocks); err != nil {
			em.fail("internal", "the answer cannot be saved")
			return
		}
		if len(resp.ToolCalls) == 0 {
			em.send("done", map[string]string{"stop": "end_turn"})
			return
		}
		var results []Block
		pending := false
		for _, c := range resp.ToolCalls {
			t, ok := toolIn(tools, c.Name)
			em.send("tool_call", map[string]any{"id": c.ID, "name": c.Name, "input": c.Input, "mutating": ok && t.Mutating})
			var res Block
			switch {
			case !ok:
				res = Block{Type: BlockToolResult, ToolUseID: c.ID, ToolName: c.Name, IsError: true, Text: "unknown_tool: the tool " + c.Name + " is not available"}
			case t.Mutating:
				id, _ := uuid.NewV7()
				if err := aidb.New(s.Pool).InsertPendingAction(context.WithoutCancel(ctx), aidb.InsertPendingActionParams{ID: id, ConversationID: convID,
					ToolCallID: c.ID, Tool: c.Name, Arguments: c.Input, CreatedAt: s.Clock.Now()}); err != nil {
					em.fail("internal", "the action cannot be saved")
					return
				}
				em.send("pending_action", PendingAction{ID: id, ToolCallID: c.ID, Tool: c.Name, Arguments: c.Input, Status: "pending", CreatedAt: s.Clock.Now()})
				pending = true
				continue
			case t.Name == "propose_change" && invalid >= maxInvalidProposals:
				res = Block{Type: BlockToolResult, ToolUseID: c.ID, ToolName: c.Name, IsError: true,
					Text: "retry_limit_reached: the proposal failed validation 3 times. Explain the issues to the user."}
			default:
				out, err := s.CallTool(ctx, t, c.Input)
				res = Block{Type: BlockToolResult, ToolUseID: c.ID, ToolName: c.Name, Text: out}
				if err != nil {
					res.IsError, res.Text = true, s.toolError(t, err)
				}
				if t.Name == "propose_change" && strings.Contains(res.Text, `"valid":false`) {
					invalid++
				}
			}
			em.send("tool_result", map[string]any{"id": c.ID, "name": c.Name, "is_error": res.IsError, "text": cutUTF8(res.Text, uiResultChars)})
			results = append(results, res)
		}
		if len(results) > 0 {
			if err := s.storeMessage(ctx, convID, "tool", results); err != nil {
				em.fail("internal", "the tool results cannot be saved")
				return
			}
		}
		if pending {
			em.send("done", map[string]string{"stop": "pending_action"})
			return
		}
	}
}

func toolIn(tools []Tool, name string) (Tool, bool) {
	for _, t := range tools {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

// prepareTurn checks the provider, the conversation and the turn lock before the stream
// starts, so that these errors are JSON answers.
func (s *Service) prepareTurn(ctx context.Context, convID uuid.UUID) (Model, func(), error) {
	m, _, err := s.Model(ctx)
	if err != nil {
		return nil, nil, err
	}
	if _, err := s.conversation(ctx, convID); err != nil {
		return nil, nil, err
	}
	unlock, ok := lockTurn(convID)
	if !ok {
		return nil, nil, errTurnRunning
	}
	return m, unlock, nil
}

func (s *Service) hasPending(ctx context.Context, convID uuid.UUID) (bool, error) {
	acts, err := aidb.New(s.Pool).ListPendingActions(ctx, convID)
	if err != nil {
		return false, err
	}
	for _, a := range acts {
		if a.Status == "pending" {
			return true, nil
		}
	}
	return false, nil
}

// SendMessage saves a user message and streams the turn.
func (s *Service) SendMessage(w http.ResponseWriter, r *http.Request, convID uuid.UUID, text string) {
	ctx := r.Context()
	m, unlock, err := s.prepareTurn(ctx, convID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	defer unlock()
	if pending, err := s.hasPending(ctx, convID); err != nil || pending {
		if err == nil {
			err = errActionsPending
		}
		httpx.WriteError(w, r, err)
		return
	}
	if err := s.storeMessage(ctx, convID, "user", []Block{{Type: BlockText, Text: text}}); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	title := strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	_ = aidb.New(s.Pool).SetConversationTitle(ctx, aidb.SetConversationTitleParams{ID: convID, Title: cutUTF8(title, 60)})
	s.runTurn(ctx, m, convID, startStream(w))
}

// DecideAction confirms or rejects a pending action and continues the turn when no action
// waits any more. A confirmed action runs with the user as actor (SI-07).
func (s *Service) DecideAction(w http.ResponseWriter, r *http.Request, convID, actionID uuid.UUID, confirm bool) {
	ctx := r.Context()
	m, unlock, err := s.prepareTurn(ctx, convID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	defer unlock()
	p := kernel.FromContext(ctx)
	status := "rejected"
	if confirm {
		status = "confirmed"
	}
	now := s.Clock.Now()
	a, err := aidb.New(s.Pool).DecidePendingAction(ctx, aidb.DecidePendingActionParams{ID: actionID, ConversationID: convID, Status: status,
		DecidedBy: &p.UserID, DecidedAt: &now})
	if errors.Is(err, pgx.ErrNoRows) {
		err = errActionNotFound
	}
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	em := startStream(w)
	res := Block{Type: BlockToolResult, ToolUseID: a.ToolCallID, ToolName: a.Tool}
	if confirm {
		t, ok := ToolByName(a.Tool)
		if !ok {
			res.IsError, res.Text = true, "unknown_tool: the tool "+a.Tool+" is not available"
		} else {
			out, err := s.CallTool(ctx, t, a.Arguments)
			res.Text = out
			if err != nil {
				res.IsError, res.Text = true, s.toolError(t, err)
			}
		}
	} else {
		res.IsError, res.Text = true, "rejected: the user rejected this action. Do not run it again unless the user asks."
	}
	if err := s.Audit.Record(ctx, nil, audit.Event{Action: "ai.action." + status, TargetType: "tool", TargetID: a.Tool,
		Details: map[string]any{"conversation_id": convID, "action_id": a.ID, "is_error": res.IsError}}); err != nil {
		s.Log.Warn("audit assistant action", "err", err)
	}
	em.send("tool_result", map[string]any{"id": a.ToolCallID, "name": a.Tool, "is_error": res.IsError, "text": cutUTF8(res.Text, uiResultChars),
		"action_id": a.ID, "status": status})
	if err := s.storeMessage(ctx, convID, "tool", []Block{res}); err != nil {
		em.fail("internal", "the tool result cannot be saved")
		return
	}
	if pending, err := s.hasPending(ctx, convID); err != nil || pending {
		em.send("done", map[string]string{"stop": "pending_action"})
		return
	}
	s.runTurn(ctx, m, convID, em)
}

func pathUUID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, httpx.Validation(httpx.FieldError{Field: name, Message: "use a UUID"})
	}
	return id, nil
}

func uuidPathParam(name string) *huma.Param {
	return &huma.Param{Name: name, In: "path", Required: true, Schema: &huma.Schema{Type: huma.TypeString, Format: "uuid"}}
}

// registerAssistant registers the assistant operations. Registration does not use s.
func registerAssistant(api huma.API, r chi.Router, s *Service) {
	viewer := httpx.MinRole(kernel.Viewer)
	type convIn struct {
		ConversationID uuid.UUID `path:"conversationId"`
	}

	huma.Register(api, httpx.Op("listAIConversations", http.MethodGet, "/api/v1/ai/conversations", viewer),
		func(ctx context.Context, _ *struct{}) (*struct{ Body ConversationList }, error) {
			if err := s.enabled(ctx); err != nil {
				return nil, err
			}
			list, err := s.Conversations(ctx)
			if err != nil {
				return nil, err
			}
			return &struct{ Body ConversationList }{Body: ConversationList{Items: list}}, nil
		})

	create := httpx.Op("createAIConversation", http.MethodPost, "/api/v1/ai/conversations", viewer)
	create.DefaultStatus = http.StatusCreated
	huma.Register(api, create, func(ctx context.Context, in *struct {
		Body struct {
			Title string `json:"title,omitempty" maxLength:"200"`
		}
	}) (*struct{ Body Conversation }, error) {
		if err := s.enabled(ctx); err != nil {
			return nil, err
		}
		c, err := s.CreateConversation(ctx, in.Body.Title)
		if err != nil {
			return nil, err
		}
		return &struct{ Body Conversation }{Body: c}, nil
	})

	huma.Register(api, httpx.Op("getAIConversation", http.MethodGet, "/api/v1/ai/conversations/{conversationId}", viewer),
		func(ctx context.Context, in *convIn) (*struct{ Body ConversationDetail }, error) {
			if err := s.enabled(ctx); err != nil {
				return nil, err
			}
			d, err := s.ConversationDetail(ctx, in.ConversationID)
			if err != nil {
				return nil, err
			}
			return &struct{ Body ConversationDetail }{Body: d}, nil
		})

	del := httpx.Op("deleteAIConversation", http.MethodDelete, "/api/v1/ai/conversations/{conversationId}", viewer)
	del.DefaultStatus = http.StatusNoContent
	huma.Register(api, del, func(ctx context.Context, in *convIn) (*struct{}, error) {
		if err := s.enabled(ctx); err != nil {
			return nil, err
		}
		return nil, s.DeleteConversation(ctx, in.ConversationID)
	})

	msgSchema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[MessageIn](), true, "")
	send := httpx.Op("sendAIMessage", http.MethodPost, "/api/v1/ai/conversations/{conversationId}/messages", viewer)
	send.Parameters = []*huma.Param{uuidPathParam("conversationId")}
	send.RequestBody = &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {Schema: msgSchema}}}
	send.Responses = httpx.RawResponse(http.StatusOK, "text/event-stream", "Events of the turn: text, tool_call, tool_result, pending_action, error and done.")
	httpx.Raw(api, r, send, func(w http.ResponseWriter, req *http.Request) {
		id, err := pathUUID(req, "conversationId")
		if err != nil {
			httpx.WriteError(w, req, err)
			return
		}
		b, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxMessageBytes))
		if err != nil {
			httpx.WriteError(w, req, httpx.Errorf(http.StatusRequestEntityTooLarge, "body_too_large", "the message is too large"))
			return
		}
		var in MessageIn
		if json.Unmarshal(b, &in) != nil || strings.TrimSpace(in.Text) == "" || len(in.Text) > 20000 {
			httpx.WriteError(w, req, httpx.Validation(httpx.FieldError{Field: "text", Message: "use 1 to 20000 characters"}))
			return
		}
		s.SendMessage(w, req, id, in.Text)
	})

	for _, d := range []struct {
		id, verb string
		confirm  bool
	}{{"confirmAIAction", "confirm", true}, {"rejectAIAction", "reject", false}} {
		op := httpx.Op(d.id, http.MethodPost, "/api/v1/ai/conversations/{conversationId}/actions/{actionId}/"+d.verb, viewer)
		op.Parameters = []*huma.Param{uuidPathParam("conversationId"), uuidPathParam("actionId")}
		op.Responses = httpx.RawResponse(http.StatusOK, "text/event-stream", "Events of the continued turn.")
		confirm := d.confirm
		httpx.Raw(api, r, op, func(w http.ResponseWriter, req *http.Request) {
			conv, err := pathUUID(req, "conversationId")
			if err != nil {
				httpx.WriteError(w, req, err)
				return
			}
			action, err := pathUUID(req, "actionId")
			if err != nil {
				httpx.WriteError(w, req, err)
				return
			}
			s.DecideAction(w, req, conv, action, confirm)
		})
	}
}
