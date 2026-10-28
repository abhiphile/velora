// Package auth implements OCI Distribution Bearer-token authentication.
package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Challenge struct{ Realm, Service, Scope string }
type TokenProvider interface {
	Token(context.Context, Challenge) (string, error)
}
type CredentialsProvider interface {
	Credentials(string) (string, string, bool)
}

func ParseChallenge(header string) (Challenge, error) {
	var result Challenge
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return result, fmt.Errorf("unsupported authentication challenge")
	}
	fields := strings.Split(strings.TrimSpace(header[len("Bearer "):]), ",")
	for _, field := range fields {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch strings.ToLower(key) {
		case "realm":
			result.Realm = value
		case "service":
			result.Service = value
		case "scope":
			result.Scope = value
		}
	}
	parsed, err := url.Parse(result.Realm)
	if err != nil || parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.Host == "" {
		return Challenge{}, fmt.Errorf("invalid bearer token realm %q", result.Realm)
	}
	return result, nil
}

type BearerProvider struct {
	client      *http.Client
	credentials CredentialsProvider
	mu          sync.Mutex
	cache       map[string]cachedToken
}
type cachedToken struct {
	value   string
	expires time.Time
}

func NewBearerProvider(client *http.Client, credentials CredentialsProvider) *BearerProvider {
	if client == nil {
		client = http.DefaultClient
	}
	return &BearerProvider{client: client, credentials: credentials, cache: make(map[string]cachedToken)}
}

func (provider *BearerProvider) Token(ctx context.Context, challenge Challenge) (string, error) {
	key := challenge.Realm + "|" + challenge.Service + "|" + challenge.Scope
	provider.mu.Lock()
	if token, ok := provider.cache[key]; ok && time.Now().Before(token.expires) {
		provider.mu.Unlock()
		return token.value, nil
	}
	provider.mu.Unlock()
	endpoint, err := url.Parse(challenge.Realm)
	if err != nil {
		return "", fmt.Errorf("parse token realm: %w", err)
	}
	query := endpoint.Query()
	if challenge.Service != "" {
		query.Set("service", challenge.Service)
	}
	if challenge.Scope != "" {
		query.Set("scope", challenge.Scope)
	}
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	if provider.credentials != nil {
		if username, password, ok := provider.credentials.Credentials(challenge.Service); ok {
			request.SetBasicAuth(username, password)
		}
	}
	response, err := provider.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("request bearer token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("token service returned HTTP %s", response.Status)
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode bearer token response: %w", err)
	}
	value := payload.Token
	if value == "" {
		value = payload.AccessToken
	}
	if value == "" {
		return "", fmt.Errorf("token response contains no token")
	}
	ttl := time.Duration(payload.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if ttl > time.Hour {
		ttl = time.Hour
	}
	provider.mu.Lock()
	provider.cache[key] = cachedToken{value: value, expires: time.Now().Add(ttl)}
	provider.mu.Unlock()
	return value, nil
}

func (provider *BearerProvider) Invalidate(challenge Challenge) {
	key := challenge.Realm + "|" + challenge.Service + "|" + challenge.Scope
	provider.mu.Lock()
	delete(provider.cache, key)
	provider.mu.Unlock()
}

// DockerConfig reads optional basic credentials from Docker's standard auth file.
type DockerConfig struct{ Path string }

func (config DockerConfig) Credentials(registry string) (string, string, bool) {
	path := config.Path
	if path == "" {
		if dir := os.Getenv("DOCKER_CONFIG"); dir != "" {
			path = filepath.Join(dir, "config.json")
		} else if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, ".docker", "config.json")
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	var payload struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return "", "", false
	}
	for key, value := range payload.Auths {
		if normalizeRegistry(key) == normalizeRegistry(registry) {
			decoded, err := base64.StdEncoding.DecodeString(value.Auth)
			if err != nil {
				continue
			}
			username, password, ok := strings.Cut(string(decoded), ":")
			if ok {
				return username, password, true
			}
		}
	}
	return "", "", false
}
func normalizeRegistry(value string) string {
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimSuffix(value, "/")
	value = strings.TrimSuffix(value, "/v1")
	if value == "registry.docker.io" || value == "registry-1.docker.io" {
		return "index.docker.io"
	}
	return value
}
