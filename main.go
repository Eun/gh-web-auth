package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cli/oauth"
	"github.com/cli/oauth/device"
	"github.com/urfave/cli/v2"
)

// Build-time variables, injected via ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

//go:embed static
var staticFiles embed.FS

// AppConfig holds all runtime configuration, populated from CLI flags / env vars.
type AppConfig struct {
	ListenAddr    string
	GHConfigDir   string
	GitHost       string
	GitHostURL    string
	APIRESTPrefix string
	GraphQLURL    string
	ClientID      string
	ClientSecret  string
	Scopes        []string
	GitProtocol   string
}

// cfg is the package-level configuration, set once in main before the server starts.
var cfg *AppConfig

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

// JSON response map keys.
const (
	keyError            = "error"
	keyAuthenticated    = "authenticated"
	keyUser             = "user"
	keyScopes           = "scopes"
	keySuccess          = "success"
	msgMethodNotAllowed = "method not allowed"
)

func jsonResponse(w http.ResponseWriter, status int, v interface{}) {
	corsHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("failed to encode JSON response: %v", err)
	}
}

// handleStatus checks whether gh is authenticated using the stored token.
func handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{keyError: msgMethodNotAllowed})
		return
	}

	token, username, err := getStoredToken(cfg.GitHost)
	if err != nil || token == "" {
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			keyAuthenticated: false,
			keyUser:          "",
			keyScopes:        "",
		})
		return
	}

	// Validate the token against the GitHub API.
	scopes, err := validateToken(token)
	if err != nil {
		log.Printf("Token validation failed: %v", err)
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			keyAuthenticated: false,
			keyUser:          username,
			keyScopes:        "",
			keyError:         "token expired or invalid",
		})
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		keyAuthenticated: true,
		keyUser:          username,
		keyScopes:        scopes,
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

	host, err := oauth.NewGitHubHost(cfg.GitHostURL)
	if err != nil {
		session.mu.Lock()
		session.active = false
		session.mu.Unlock()
		cancel()
		return "", "", fmt.Errorf("invalid host: %w", err)
	}

	// Step 1: Request the device code from GitHub.
	codeResp, err := device.RequestCode(http.DefaultClient, host.DeviceCodeURL, cfg.ClientID, cfg.Scopes)
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
			ClientID:   cfg.ClientID,
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
		if saveErr := saveToken(cfg.GitHost, username, accessToken.Token, cfg.GitProtocol); saveErr != nil {
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
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{keyError: msgMethodNotAllowed})
		return
	}

	code, url, err := startDeviceFlow()
	if err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "login already in progress" {
			status = http.StatusConflict
		}
		jsonResponse(w, status, map[string]string{keyError: err.Error()})
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
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{keyError: msgMethodNotAllowed})
		return
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	if !session.active {
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"done":     false,
			keySuccess: false,
			keyError:   "no active login session",
		})
		return
	}

	resp := map[string]interface{}{
		"done":     session.done,
		keySuccess: session.success,
		keyError:   session.errMsg,
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
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{keyError: msgMethodNotAllowed})
		return
	}

	err := removeHost(cfg.GitHost)
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		keySuccess: err == nil,
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
		jsonResponse(w, http.StatusMethodNotAllowed, map[string]string{keyError: msgMethodNotAllowed})
		return
	}

	killActiveSession()
	_ = removeHost(cfg.GitHost)

	code, url, err := startDeviceFlow()
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{keyError: err.Error()})
		return
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"code": code,
		"url":  url,
	})
}

func main() {
	app := &cli.App{
		Name:    "gh-web-auth",
		Usage:   "Web UI for GitHub CLI authentication via OAuth device flow",
		Version: version + " (" + commit + ") " + date,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "listen-addr",
				EnvVars: []string{"GH_WEB_AUTH_LISTEN_ADDR"},
				Value:   defaultListenAddr,
				Usage:   "Address to listen on",
			},
			&cli.StringFlag{
				Name:    "gh-config-dir",
				EnvVars: []string{"GH_CONFIG_DIR"},
				Value:   "",
				Usage:   "gh config directory",
			},
			&cli.StringFlag{
				Name:    "gh-host",
				EnvVars: []string{"GH_HOST"},
				Value:   defaultGitHost,
				Usage:   "GitHub hostname",
			},
			&cli.StringFlag{
				Name:    "gh-client-id",
				EnvVars: []string{"GH_CLIENT_ID"},
				Value:   defaultClientID,
				Usage:   "OAuth client ID",
			},
			&cli.StringFlag{
				Name:    "gh-client-secret",
				EnvVars: []string{"GH_CLIENT_SECRET"},
				Value:   defaultClientSecret,
				Usage:   "OAuth client secret",
			},
			&cli.StringFlag{
				Name:    "gh-scopes",
				EnvVars: []string{"GH_SCOPES"},
				Value:   defaultScopes,
				Usage:   "Comma-separated OAuth scopes",
			},
			&cli.StringFlag{
				Name:    "gh-git-protocol",
				EnvVars: []string{"GH_GIT_PROTOCOL"},
				Value:   defaultGitProtocol,
				Usage:   "Git protocol (https or ssh)",
			},
		},
		Action: func(c *cli.Context) error {
			host := c.String("gh-host")

			// Derive host-dependent URLs.
			gitHostURL := "https://" + host
			var apiRESTPrefix, graphQLURL string
			if host == "github.com" {
				apiRESTPrefix = "https://api.github.com/"
				graphQLURL = "https://api.github.com/graphql"
			} else {
				apiRESTPrefix = "https://" + host + "/api/v3/"
				graphQLURL = "https://" + host + "/api/graphql"
			}

			cfg = &AppConfig{
				ListenAddr:    c.String("listen-addr"),
				GHConfigDir:   c.String("gh-config-dir"),
				GitHost:       host,
				GitHostURL:    gitHostURL,
				APIRESTPrefix: apiRESTPrefix,
				GraphQLURL:    graphQLURL,
				ClientID:      c.String("gh-client-id"),
				ClientSecret:  c.String("gh-client-secret"),
				Scopes:        strings.Split(c.String("gh-scopes"), ","),
				GitProtocol:   c.String("gh-git-protocol"),
			}

			// Propagate GH_CONFIG_DIR so ghconfig.go picks it up.
			if cfg.GHConfigDir != "" {
				os.Setenv("GH_CONFIG_DIR", cfg.GHConfigDir)
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
				Addr:              cfg.ListenAddr,
				Handler:           mux,
				ReadHeaderTimeout: 10 * time.Second,
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
				if err := srv.Shutdown(ctx); err != nil {
					log.Printf("server shutdown error: %v", err)
				}
			}()

			log.Printf("Starting gh-web-auth on %s", cfg.ListenAddr)
			if err := srv.ListenAndServe(); err != http.ErrServerClosed {
				return err
			}
			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
