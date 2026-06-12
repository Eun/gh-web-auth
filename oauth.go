package main

// These are the "GitHub CLI" OAuth app credentials.
// Extracted from github.com/cli/cli/v2 internal/authflow/flow.go.
// They are safe to embed in version control.
var (
	oauthClientID     = "178c6fc778ccc68e1d6a"
	oauthClientSecret = "34ddeff2b558a23d38fba8a6de74f086ede1cc0b"

	// Default scopes matching what `gh auth login --web -p https` requests.
	defaultScopes = []string{"repo", "read:org", "gist", "workflow"}

	oauthHost     = "github.com"
	oauthHostURL  = "https://github.com"
	callbackURI   = "http://127.0.0.1/callback"
	apiRESTPrefix = "https://api.github.com/"
	graphqlURL    = "https://api.github.com/graphql"
)
