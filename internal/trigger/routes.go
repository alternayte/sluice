package trigger

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alternayte/sluice/internal/kernel"
	"github.com/alternayte/sluice/internal/platform/httpx"
)

// WebhookAccepted is the response of a webhook call.
type WebhookAccepted struct {
	ExecutionID uuid.UUID `json:"execution_id"`
}

// WebhookKey is a new webhook key. The server shows it once.
type WebhookKey struct {
	Key string `json:"key"`
	URL string `json:"url"`
}

// UpcomingSchedule is the next fire time of one schedule trigger.
type UpcomingSchedule struct {
	Namespace  string    `json:"namespace"`
	FlowID     string    `json:"flow_id"`
	TriggerID  string    `json:"trigger_id"`
	Cron       string    `json:"cron"`
	Timezone   string    `json:"timezone"`
	NextFireAt time.Time `json:"next_fire_at"`
}

// UpcomingList is the list of next fire times, soonest first.
type UpcomingList struct {
	Items []UpcomingSchedule `json:"items"`
}

// Routes registers the webhook, key rotation and upcoming schedule operations.
func Routes(api huma.API, r chi.Router, s *Service) {
	hook := httpx.Op("fireWebhook", http.MethodPost, "/hooks/{key}", httpx.Public)
	hook.Summary = "Start the flow of a webhook trigger"
	hook.Parameters = httpx.PathParams("key")
	hook.Responses = httpx.RawResponse(http.StatusAccepted, "application/json", "The execution was created.")
	httpx.Raw(api, r, hook, func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, MaxWebhookBody))
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.WriteError(w, req, ErrBodyTooLarge)
			return
		}
		if err != nil {
			httpx.WriteError(w, req, httpx.Errorf(http.StatusBadRequest, "bad_request", "read body: %v", err))
			return
		}
		id, err := s.FireWebhook(req.Context(), chi.URLParam(req, "key"), body, req.Header)
		if err != nil {
			httpx.WriteError(w, req, err)
			return
		}
		httpx.WriteJSON(w, http.StatusAccepted, WebhookAccepted{ExecutionID: id})
	})

	huma.Register(api, httpx.Op("rotateWebhookKey", http.MethodPost, "/api/v1/flows/{namespace}/{flowId}/triggers/{triggerId}/webhook-key",
		httpx.MinRole(kernel.Editor)),
		func(ctx context.Context, in *struct {
			Namespace string `path:"namespace" maxLength:"128" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$"`
			FlowID    string `path:"flowId" maxLength:"63" pattern:"^[a-z0-9][a-z0-9-]*$"`
			TriggerID string `path:"triggerId" maxLength:"63"`
		}) (*struct{ Body WebhookKey }, error) {
			key, err := s.RotateWebhookKey(ctx, in.Namespace, in.FlowID, in.TriggerID)
			if err != nil {
				return nil, err
			}
			return &struct{ Body WebhookKey }{Body: WebhookKey{Key: key, URL: s.WebhookURL(key)}}, nil
		})

	huma.Register(api, httpx.Op("listUpcomingSchedules", http.MethodGet, "/api/v1/schedules/upcoming", httpx.MinRole(kernel.Viewer)),
		func(ctx context.Context, in *struct {
			Namespace string `query:"namespace" doc:"Namespace and its children."`
			Limit     int    `query:"limit" minimum:"1" maximum:"200" default:"10"`
		}) (*struct{ Body UpcomingList }, error) {
			list, err := s.Upcoming(ctx, in.Namespace, in.Limit)
			if err != nil {
				return nil, err
			}
			out := &struct{ Body UpcomingList }{Body: UpcomingList{Items: []UpcomingSchedule{}}}
			for _, u := range list {
				out.Body.Items = append(out.Body.Items, UpcomingSchedule{Namespace: u.Namespace, FlowID: u.FlowKey, TriggerID: u.TriggerKey,
					Cron: u.Cron, Timezone: u.Timezone, NextFireAt: u.NextFireAt})
			}
			return out, nil
		})
}
