package app

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alternayte/sluice/internal/execution"
	"github.com/alternayte/sluice/internal/execution/executiondb"
	"github.com/alternayte/sluice/internal/trigger"
)

// triggerStarter adapts the execution engine to trigger.Starter.
type triggerStarter struct{ e *execution.Engine }

func (t triggerStarter) Start(ctx context.Context, tx pgx.Tx, s trigger.Start) (uuid.UUID, error) {
	id := s.TriggerID
	return t.e.TriggerTx(ctx, tx, execution.TriggerParams{Namespace: s.Namespace, FlowKey: s.FlowKey, TriggerType: s.TriggerType,
		TriggerID: &id, ScheduledFor: s.ScheduledFor, TriggerPayload: s.Payload, InputTemplates: s.InputTemplates, ChainDepth: s.ChainDepth})
}

// flowTriggerHook fires flow triggers in the transaction that ends an execution.
func flowTriggerHook(t *trigger.Service) execution.EndHook {
	return func(ctx context.Context, tx pgx.Tx, ex executiondb.Execution) error {
		return t.OnEnd(ctx, tx, trigger.Ended{ID: ex.ID, FlowID: ex.FlowID, State: ex.State, Outputs: ex.Outputs, ChainDepth: int(ex.ChainDepth)})
	}
}
