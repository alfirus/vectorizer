package security

// Workspace scoping helper (F2). The auth middleware in main.go enforces
// the JWT workspace claim against route params and the query string, but
// workspaces can also arrive in JSON bodies (search filters, creates) or
// be resolved server-side (message :id -> owning workspace). Handlers use
// Allows to enforce the claim against those resolved values, and to
// restrict fan-out endpoints (search-all) to the caller's workspace.

// Allows reports whether claims permit access to wsID.
// Nil claims (no JWT mode, e.g. API-key auth) and admin claims allow
// everything. A scoped non-admin claim allows exactly its own workspace;
// an empty wsID is denied for scoped tokens (fail closed — the caller
// must name the workspace it wants).
func (c *Claims) Allows(wsID string) bool {
	if c == nil {
		return true
	}
	if c.Admin {
		return true
	}
	if c.Workspace == "" {
		return true
	}
	return wsID != "" && wsID == c.Workspace
}
