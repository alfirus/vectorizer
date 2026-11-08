// Package version is the single source of truth for the Vectorizer
// release version. Every surface that reports a version (REST /health,
// gRPC Health, MCP manifest) must derive from Version, never a
// hardcoded string. Canonical version: 0.4.0 (P0-1).
package version

// Version is the current Vectorizer release.
const Version = "0.4.0"
