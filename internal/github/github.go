// Package github implements the trusted GitHub adapter: GitHub App
// authentication, the REST surface RepoSurgeon needs, and webhook signature
// verification with replay protection.
//
// Trust boundary (docs/adr/ADR-001): this package is the only component that
// holds the App private key or installation tokens. The runner never receives
// either, and model output can never reach these methods.
package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultAPIBaseURL is the GitHub REST API base. Tests override it with a local
// httptest server.
const DefaultAPIBaseURL = "https://api.github.com"

// Installation is a GitHub App installation bound to an account.
type Installation struct {
	ID      int64  `json:"id"`
	Login   string `json:"account_login"`
	AppSlug string `json:"app_slug"`
}

// Repository is a repository the installation can access.
type Repository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
	HTMLURL       string `json:"html_url"`
}

// Issue is an imported GitHub issue snapshot.
type Issue struct {
	ID        int64     `json:"id"`
	Number    int64     `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	UserLogin string    `json:"user_login"`
}

// Comment is an issue comment snapshot.
type Comment struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UserLogin string    `json:"user_login"`
}

// HTTPClient bounds every GitHub call.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Config configures the GitHub adapter.
type Config struct {
	AppID         int64
	PrivateKeyPEM []byte
	APIBaseURL    string
	HTTPClient    HTTPClient
	// TokenTTL bounds cached installation tokens.
	TokenTTL time.Duration
}

// Adapter is the trusted GitHub client.
type Adapter struct {
	appID      int64
	privateKey *rsa.PrivateKey
	apiBaseURL string
	httpClient HTTPClient
	tokenTTL   time.Duration
	tokenCache map[int64]cachedToken
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// NewAdapter validates configuration and prepares the App JWT signer.
func NewAdapter(cfg Config) (*Adapter, error) {
	if cfg.AppID == 0 {
		return nil, errors.New("github: app id is required")
	}
	if len(cfg.PrivateKeyPEM) == 0 {
		return nil, errors.New("github: app private key is required")
	}
	key, err := parsePrivateKey(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	base := cfg.APIBaseURL
	if base == "" {
		base = DefaultAPIBaseURL
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Adapter{
		appID:      cfg.AppID,
		privateKey: key,
		apiBaseURL: strings.TrimSuffix(base, "/"),
		httpClient: client,
		tokenTTL:   orDefaultDuration(cfg.TokenTTL, 50*time.Minute),
		tokenCache: map[int64]cachedToken{},
	}, nil
}

func orDefaultDuration(v, def time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return def
}

// appJWT mints a short-lived GitHub App JWT.
func (a *Adapter) appJWT(now time.Time) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims := fmt.Sprintf(`{"iat":%d,"exp":%d,"iss":%d}`,
		now.Add(-30*time.Second).Unix(), now.Add(9*time.Minute).Unix(), a.appID)
	encodedClaims := base64.RawURLEncoding.EncodeToString([]byte(claims))
	signingInput := header + "." + encodedClaims

	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, a.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("github: sign app jwt: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// installationToken returns a cached token for the installation or exchanges
// the App JWT for a fresh one.
func (a *Adapter) installationToken(ctx context.Context, installationID int64) (string, error) {
	now := time.Now()
	if cached, ok := a.tokenCache[installationID]; ok && now.Before(cached.expiresAt.Add(-30*time.Second)) {
		return cached.token, nil
	}
	jwt, err := a.appJWT(now)
	if err != nil {
		return "", err
	}

	var payload struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := a.doJSON(http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", installationID), jwt, nil, &payload); err != nil {
		return "", err
	}
	if payload.Token == "" {
		return "", errors.New("github: installation token response carried no token")
	}
	expiry := payload.ExpiresAt
	if expiry.IsZero() {
		expiry = now.Add(a.tokenTTL)
	}
	a.tokenCache[installationID] = cachedToken{token: payload.Token, expiresAt: expiry}
	return payload.Token, nil
}

// InstallationRepositories lists repositories the installation can access.
func (a *Adapter) InstallationRepositories(ctx context.Context, installationID int64) ([]Repository, error) {
	token, err := a.installationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Repositories []Repository `json:"repositories"`
	}
	if err := a.doJSON(http.MethodGet, "/installation/repositories", token, nil, &payload); err != nil {
		return nil, err
	}
	return payload.Repositories, nil
}

// GetIssue fetches an issue snapshot for an owner/repo pair.
func (a *Adapter) GetIssue(ctx context.Context, installationID int64, owner, repo string, number int64) (Issue, error) {
	token, err := a.installationToken(ctx, installationID)
	if err != nil {
		return Issue{}, err
	}
	var issue Issue
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number)
	if err := a.doJSON(http.MethodGet, path, token, nil, &issue); err != nil {
		return Issue{}, err
	}
	return issue, nil
}

// ListIssueComments fetches comments for an issue.
func (a *Adapter) ListIssueComments(ctx context.Context, installationID int64, owner, repo string, number int64) ([]Comment, error) {
	token, err := a.installationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	var comments []Comment
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number)
	if err := a.doJSON(http.MethodGet, path, token, nil, &comments); err != nil {
		return nil, err
	}
	return comments, nil
}

// GetBranch fetches branch metadata, used to detect protected branches.
func (a *Adapter) GetBranch(ctx context.Context, installationID int64, owner, repo, branch string) (map[string]any, error) {
	token, err := a.installationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	path := fmt.Sprintf("/repos/%s/%s/branches/%s", owner, repo, branch)
	if err := a.doJSON(http.MethodGet, path, token, nil, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// doJSON performs one authenticated GitHub request and decodes the response.
func (a *Adapter) doJSON(method, path, token string, body, out any) error {
	return a.doJSONContext(context.Background(), method, path, token, body, out)
}

func (a *Adapter) doJSONContext(ctx context.Context, method, path, token string, body, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("github: encode request: %w", err)
		}
		reader = strings.NewReader(string(payload))
	}
	req, err := http.NewRequestWithContext(ctx, method, a.apiBaseURL+path, reader)
	if err != nil {
		return fmt.Errorf("github: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("github: request %s %s: %w", method, path, err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			// Closing failures after a full read are non-actionable.
			_ = closeErr
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("github: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{
			Status:  resp.StatusCode,
			Message: summarize(raw),
		}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("github: decode response: %w", err)
	}
	return nil
}

// APIError is a GitHub API failure with its status.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github: api error %d: %s", e.Status, e.Message)
}

// PermissionDenied reports whether the error is a GitHub 403/404.
func (e *APIError) PermissionDenied() bool {
	return e.Status == http.StatusForbidden || e.Status == http.StatusNotFound
}

// RateLimited reports whether the error is a GitHub 429.
func (e *APIError) RateLimited() bool { return e.Status == http.StatusTooManyRequests }

func summarize(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) > 300 {
		return trimmed[:300]
	}
	return trimmed
}

// parsePrivateKey decodes a PKCS#1 or PKCS#8 PEM RSA key.
func parsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("github: private key is not valid PEM")
	}
	if key, err := parsePKCS1(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := parsePKCS8(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github: parse private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github: private key is not an RSA key")
	}
	return key, nil
}

// parsePKCS1 decodes a PKCS#1 RSA private key.
func parsePKCS1(der []byte) (*rsa.PrivateKey, error) {
	key, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, err
	}
	return key, nil
}

// parsePKCS8 decodes a PKCS#8 private key.
func parsePKCS8(der []byte) (any, error) {
	return x509.ParsePKCS8PrivateKey(der)
}
