package main

// Default OAuth credentials — the official "GitHub CLI" OAuth app.
// Extracted from github.com/cli/cli/v2 internal/authflow/flow.go.
const (
	defaultClientID     = "178c6fc778ccc68e1d6a"
	defaultClientSecret = "34ddeff2b558a23d38fba8a6de74f086ede1cc0b" //nolint:gosec // Public OAuth app creds.
	defaultGitHost      = "github.com"
	defaultScopes       = "repo,read:org,gist,workflow"
	defaultGitProtocol  = ""
	defaultListenAddr   = "0.0.0.0:8080"
)
