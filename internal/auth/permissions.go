package auth

// Access is the permission of one route or operation (SI-03). Default deny: a route
// without an entry is rejected and fails the route inventory test.
type Access struct {
	// Public routes need no authentication.
	Public bool
	// Min is the lowest role that can call the route.
	Min Role
	// Self routes are allowed while the user must change the password.
	Self bool
	// Other describes routes with their own authentication (run tokens, webhook keys).
	Other string
}

var (
	public        = Access{Public: true}
	authenticated = Access{Min: Viewer, Self: true}
	viewer        = Access{Min: Viewer}
	editor        = Access{Min: Editor}
	admin         = Access{Min: Admin}
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
}

// OperationKey normalizes an operationId. The spec embedded by oapi-codegen has
// capitalized operation IDs (Login), the source spec has lower camel case (login).
func OperationKey(id string) string {
	if id == "" || id[0] < 'A' || id[0] > 'Z' {
		return id
	}
	return string(id[0]+('a'-'A')) + id[1:]
}

// Routes maps router patterns that are not OpenAPI operations to their permission.
var Routes = map[string]Access{
	"GET /healthz": public,
	"GET /readyz":  public,
	"GET /metrics": public,
	"/api/":        {Public: true, Other: "JSON 404 for unknown API routes"},
	"/":            {Public: true, Other: "embedded UI"},
}
