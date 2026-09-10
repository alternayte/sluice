package auth

import "github.com/alternayte/sluice/internal/kernel"

// Access is the permission of one route or operation (SI-03). Default deny: a route
// without an entry is rejected and fails the route inventory test.
type Access struct {
	// Public routes need no authentication.
	Public bool
	// Min is the lowest role that can call the route.
	Min kernel.Role
	// Self routes are allowed while the user must change the password.
	Self bool
	// Other describes routes with their own authentication (run tokens, webhook keys).
	Other string
}

var (
	public        = Access{Public: true}
	authenticated = Access{Min: kernel.Viewer, Self: true}
	viewer        = Access{Min: kernel.Viewer}
	operator      = Access{Min: kernel.Operator}
	editor        = Access{Min: kernel.Editor}
	runToken      = Access{Other: "run token of the task run (SI-04)"}
	admin         = Access{Min: kernel.Admin}
)

// Operations maps each OpenAPI operationId to its permission (Appendix B).
var Operations = map[string]Access{
	// Session and profile. Own profile, password and tokens: all roles.
	"login":               public,
	"logout":              authenticated,
	"getMe":               authenticated,
	"updateMe":            authenticated,
	"changePassword":      authenticated,
	"revokeOtherSessions": authenticated,
	"listTokens":          viewer, // all=true needs admin, checked in the handler
	"createToken":         viewer, // role must not exceed the owner role
	"revokeToken":         viewer, // owner or admin, checked in the handler

	// Admin: users, audit log, instances (settings).
	"listUsers":         admin,
	"createUser":        admin,
	"updateUser":        admin,
	"resetUserPassword": admin,
	"listAuditEvents":   admin,
	"listInstances":     admin,

	// Namespaces and files. Read: viewer. Managed edits and namespace creation: editor.
	// Namespace delete: admin.
	"listNamespaces":  viewer,
	"getNamespace":    viewer,
	"createNamespace": editor,
	"deleteNamespace": admin,
	"listFiles":       viewer,
	"getFile":         viewer,
	"uploadFile":      editor,
	"saveChanges":     editor,
	"listVersions":    viewer,
	"diffVersions":    viewer,
	"revertVersion":   editor,
	"validateFile":    viewer,

	// Flows. Enable and disable: editor.
	"listFlows":         viewer,
	"getFlow":           viewer,
	"updateFlow":        editor,
	"listFlowRevisions": viewer,
	"getFlowRevision":   viewer,
	"diffFlowRevisions": viewer,
	"getFlowSchema":     public,

	// Executions. Trigger, cancel, rerun, restart and run file: operator.
	"triggerFlow":            operator,
	"runFile":                operator,
	"listExecutions":         viewer,
	"getExecution":           viewer,
	"cancelExecution":        operator,
	"rerunExecution":         operator,
	"restartExecution":       operator,
	"getExecutionLogs":       viewer,
	"streamExecutionLogs":    viewer,
	"downloadExecutionLogs":  viewer,
	"streamExecutionEvents":  viewer,
	"listExecutionMetrics":   viewer,
	"listExecutionArtifacts": viewer,
	"downloadArtifact":       viewer,

	// Runner protocol (Appendix C): bearer run token, checked by the runner middleware.
	"runnerGetSpec":     runToken,
	"runnerGetBundle":   runToken,
	"runnerPostLogs":    runToken,
	"runnerPostEvents":  runToken,
	"runnerPutArtifact": runToken,
	"runnerHeartbeat":   runToken,
	"runnerComplete":    runToken,
}

// OperationKey normalizes an operationId. The spec embedded by oapi-codegen has
// capitalized operation IDs (Login), the source spec has lower camel case (login).
func OperationKey(id string) string {
	if id == "" || id[0] < 'A' || id[0] > 'Z' {
		return id
	}
	return string(id[0]+('a'-'A')) + id[1:]
}

// RoutePatterns maps router patterns that are not OpenAPI operations to their permission.
var RoutePatterns = map[string]Access{
	"GET /healthz": public,
	"GET /readyz":  public,
	"GET /metrics": public,
	"/api/":        {Public: true, Other: "JSON 404 for unknown API routes"},
	"/":            {Public: true, Other: "embedded UI"},
}
