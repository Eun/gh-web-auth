package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// validateToken checks whether the given token is valid by calling the GitHub API.
// Returns the X-Oauth-Scopes header value and any error.
func validateToken(token string) (scopes string, err error) {
	req, err := http.NewRequest("GET", apiRESTPrefix, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "token "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token validation request failed: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("token invalid (HTTP %d)", resp.StatusCode)
	}

	return resp.Header.Get("X-Oauth-Scopes"), nil
}

// getViewerLogin queries the GitHub GraphQL API for the authenticated user's login name.
func getViewerLogin(token string) (string, error) {
	body := `{"query":"query { viewer { login } }"}`
	req, err := http.NewRequest("POST", graphqlURL, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("graphql request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("graphql request returned HTTP %d", resp.StatusCode)
	}

	var result struct {
		Data struct {
			Viewer struct {
				Login string `json:"login"`
			} `json:"viewer"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode graphql response: %w", err)
	}

	if len(result.Errors) > 0 {
		return "", fmt.Errorf("graphql error: %s", result.Errors[0].Message)
	}

	if result.Data.Viewer.Login == "" {
		return "", fmt.Errorf("could not determine username")
	}

	return result.Data.Viewer.Login, nil
}
