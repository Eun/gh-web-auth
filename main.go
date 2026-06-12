package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/cli/oauth"
	"github.com/cli/oauth/device"
)

//go:embed static
var staticFiles embed.FS

// LoginSession holds state for an in-progress device flow.
type LoginSession struct {
	mu      sync.Mutex
	code    string // user-facing device code
	url     string // verification URL
	done    bool
	success bool
	token   string
	errMsg  string
	active  bool
	cancel  context.CancelFunc
}

var session = &LoginSession{}

func corsHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func jsonResponse(w http.ResponseWriter, status int, v interface{}) {
	corsHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// handleStatus checks whether gh is authenticated using the stored token.
func handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	token, username, err := getStoredToken(oauthHost)
	if err != nil || token == "" {
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"authenticated": false,
			"user":          "",
			"scopes":        "",
		})
		return
	}

	// Validate the token against the GitHub API.
	scopes, err := validateToken(token)
	if err != nil {
		log.Printf("Token validation failed: %v", err)
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"authenticated": false,
			"user":          username,
			"scopes":        "",
			"error":         "token expired or invalid",
		})
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"authenticated": true,
		"user":          username,
		"scopes":        scopes,
	})
}

// startDeviceFlow performs the GitHub OAuth device flow using the cli/oauth library
// (the same library the official gh CLI uses). It returns the device code and
// verification URL so the web UI can display them.
func startDeviceFlow() (code, url string, err error) {
	session.mu.Lock()
	if session.active {
		session.mu.Unlock()
		return "", "", fmt.Errorf("login already in progress")
	}

	// Reset session state.
	session.code = ""
	session.url = ""
	session.done = false
	session.success = false
	session.token = ""
	session.errMsg = ""
	session.active = true

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	session.cancel = cancel
	session.mu.Unlock()

	host, err := oauth.NewGitHubHost(oauthHostURL)
	if err != nil {
		session.mu.Lock()
		session.active = false
		session.mu.Unlock()
		cancel()
		return "", "", fmt.Errorf("invalid host: %w", err)
	}

	// Step 1: Request the device code from GitHub.
	codeResp, err := device.RequestCode(http.DefaultClient, host.DeviceCodeURL, oauthClientID, defaultScopes)
	if err != nil {
		session.mu.Lock()
		session.active = false
		session.mu.Unlock()
		cancel()
		return "", "", fmt.Errorf("device code request failed: %w", err)
	}

	session.mu.Lock()
	session.code = codeResp.UserCode
	session.url = codeResp.VerificationURI
	session.mu.Unlock()

	// Step 2: Poll for the token in the background.
	go func() {
		defer cancel()

		accessToken, err := device.Wait(ctx, http.DefaultClient, host.TokenURL, device.WaitOptions{
			ClientID:   oauthClientID,
			DeviceCode: codeResp,
		})

		session.mu.Lock()
		defer session.mu.Unlock()
		session.done = true

		if err != nil {
			session.success = false
			session.errMsg = err.Error()
			return
		}

		session.token = accessToken.Token

		// Look up username via GraphQL.
		username, lookupErr := getViewerLogin(accessToken.Token)
		if lookupErr != nil {
			log.Printf("Warning: could not get viewer login: %v", lookupErr)
			username = "unknown"
		}

		// Store the token in gh's config format.
		if saveErr := saveToken(oauthHost, username, accessToken.Token, "https"); saveErr != nil {
			session.success = false
			session.errMsg = fmt.Sprintf("token obtained but failed to save: %v", saveErr)
			return
		}

		session.success = true
		log.Printf("Authentication successful for user %s", username)
	}()

	return codeResp.UserCode, codeResp.VerificationURI, nil
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	code, url, err := startDeviceFlow()
	if err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "login already in progress" {
			status = http.StatusConflict
		}
		jsonResponse(w, status, map[string]string{"error": err.Error()})
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"code": code,
		"url":  url,
	})
}

func handlePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	if !session.active {
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"done":    false,
			"success": false,
			"error":   "no active login session",
		})
		return
	}

	resp := map[string]interface{}{
		"done":    session.done,
		"success": session.success,
		"error":   session.errMsg,
	}

	if session.done {
		session.active = false
	}

	jsonResponse(w, http.StatusOK, resp)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	err := removeHost(oauthHost)
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": err == nil,
	})
}

// killActiveSession cancels any running login flow.
func killActiveSession() {
	session.mu.Lock()
	if session.active && session.cancel != nil {
		session.cancel()
	}
	session.active = false
	session.done = false
	session.mu.Unlock()

	time.Sleep(200 * time.Millisecond)
}

func handleReauth(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	killActiveSession()
	_ = removeHost(oauthHost)

	code, url, err := startDeviceFlow()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"code": code,
		"url":  url,
	})
}

func main() {
	daemon := flag.Bool("daemon", false, "Run as daemon (use systemd for process management)")
	flag.Parse()

	if *daemon {
		log.Println("Daemon mode: use systemd or similar for process management")
	}

	mux := http.NewServeMux()

	// Serve embedded static files.
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("failed to create sub filesystem: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	mux.HandleFunc("/api/status", handleStatus)
	mux.HandleFunc("/api/login", handleLogin)
	mux.HandleFunc("/api/login/poll", handlePoll)
	mux.HandleFunc("/api/logout", handleLogout)
	mux.HandleFunc("/api/reauth", handleReauth)

	srv := &http.Server{
		Addr:    "0.0.0.0:8080",
		Handler: mux,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		killActiveSession()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Println("Starting gh-web-auth on 0.0.0.0:8080")
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
