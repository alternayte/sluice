package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/alternayte/sluice/internal/platform/httpx"
)

// maxAttachments limits the attachments of one message.
const maxAttachments = 5

// Attachment is an object that the user attaches to a message with an @ mention.
type Attachment struct {
	Kind        string `json:"kind" enum:"flow,execution,file"`
	Namespace   string `json:"namespace,omitempty" doc:"Namespace of a flow or a file."`
	FlowID      string `json:"flow_id,omitempty" doc:"Flow ID of a flow."`
	ExecutionID string `json:"execution_id,omitempty" doc:"Execution UUID."`
	Path        string `json:"path,omitempty" doc:"File path of a file."`
}

// failedLogTail is the number of failed-task log lines that an execution attachment holds.
const failedLogTail = 100

// expandAttachments reads each attached object through the read tools, so the role check,
// the masking and the size limit of the tools apply (SI-01). An object that the user cannot
// read fails the message.
func (s *Service) expandAttachments(ctx context.Context, list []Attachment) ([]Block, error) {
	out := make([]Block, 0, len(list))
	for i, a := range list {
		var label string
		var parts []string
		var err error
		switch a.Kind {
		case "flow":
			label = "flow " + a.Namespace + "/" + a.FlowID
			parts, err = s.readTools(ctx, [][2]any{{"get_flow", map[string]any{"namespace": a.Namespace, "flow_id": a.FlowID}}})
		case "file":
			label = "file " + a.Namespace + ":" + a.Path
			parts, err = s.readTools(ctx, [][2]any{{"read_file", map[string]any{"namespace": a.Namespace, "path": a.Path}}})
		case "execution":
			label = "execution " + a.ExecutionID
			id := map[string]any{"execution_id": a.ExecutionID}
			parts, err = s.readTools(ctx, [][2]any{
				{"get_execution", id},
				{"get_insight", id},
				{"get_logs", map[string]any{"execution_id": a.ExecutionID, "failed_only": true, "tail": failedLogTail}},
			})
		default:
			return nil, httpx.Validation(httpx.FieldError{Field: fmt.Sprintf("attachments[%d].kind", i), Message: "use flow, execution or file"})
		}
		if err != nil {
			return nil, err
		}
		out = append(out, Block{Type: BlockAttachment, Label: label, Text: strings.Join(parts, "\n")})
	}
	return out, nil
}

// readTools calls read tools by name and returns their results, each after a heading line.
func (s *Service) readTools(ctx context.Context, calls [][2]any) ([]string, error) {
	var out []string
	for _, c := range calls {
		name := c[0].(string)
		t, ok := toolIn(Tools(), name)
		if !ok || t.Mutating {
			return nil, fmt.Errorf("attachment tool %s is not a read tool", name)
		}
		in, err := json.Marshal(c[1])
		if err != nil {
			return nil, err
		}
		text, err := s.CallTool(ctx, t, in)
		if err != nil {
			var he *httpx.Error
			if errors.As(err, &he) && he.Status < http.StatusInternalServerError {
				return nil, httpx.Errorf(he.Status, he.Code, "attachment: %s", he.Message)
			}
			return nil, err
		}
		out = append(out, name+":", text)
	}
	return out, nil
}
