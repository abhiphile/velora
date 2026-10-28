// Package client implements the OCI Distribution HTTP protocol used by the puller.
package client

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vellora/vellora/internal/image/registry/auth"
	"github.com/vellora/vellora/internal/image/registry/blob"
)

type TokenProvider interface {
	Token(context.Context, auth.Challenge) (string, error)
}
type tokenInvalidator interface{ Invalidate(auth.Challenge) }
type RegistryClient interface {
	Ping(context.Context, string, string) error
	Get(context.Context, string, string, string, int64) (*http.Response, error)
}
type ManifestClient interface {
	FetchManifest(context.Context, string, string, string, string) (io.ReadCloser, string, error)
}

type HTTP struct {
	client  *http.Client
	tokens  TokenProvider
	logger  *slog.Logger
	retries int
}

func New(httpClient *http.Client, tokens TokenProvider, logger *slog.Logger) (*HTTP, error) {
	if logger == nil {
		return nil, fmt.Errorf("registry client requires a logger")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Minute}
	}
	return &HTTP{client: httpClient, tokens: tokens, logger: logger, retries: 4}, nil
}

func (registry *HTTP) Ping(ctx context.Context, base, scope string) error {
	response, err := registry.request(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/v2/", scope, "", 0)
	if err != nil {
		return fmt.Errorf("ping registry %s: %w", base, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("registry ping returned HTTP %s", response.Status)
	}
	return nil
}

func (registry *HTTP) Get(ctx context.Context, endpoint, scope, accept string, offset int64) (*http.Response, error) {
	return registry.request(ctx, http.MethodGet, endpoint, scope, accept, offset)
}

func (registry *HTTP) FetchManifest(ctx context.Context, base, repository, reference, scope string) (io.ReadCloser, string, error) {
	endpoint, err := blob.ManifestEndpoint(base, repository, reference)
	if err != nil {
		return nil, "", err
	}
	accept := "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"
	response, err := registry.request(ctx, http.MethodGet, endpoint, scope, accept, 0)
	if err != nil {
		return nil, "", err
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		response.Body.Close()
		return nil, "", fmt.Errorf("parse registry manifest media type: %w", err)
	}
	return response.Body, mediaType, nil
}

func (registry *HTTP) request(ctx context.Context, method, endpoint, scope, accept string, offset int64) (*http.Response, error) {
	var token string
	refreshedToken := false
	for attempt := 0; attempt < registry.retries; attempt++ {
		request, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("create registry request: %w", err)
		}
		if accept != "" {
			request.Header.Set("Accept", accept)
		}
		request.Header.Set("User-Agent", "vellora/0.1")
		if offset > 0 {
			request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := registry.client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("registry request canceled: %w", ctx.Err())
			}
			if attempt+1 == registry.retries {
				return nil, fmt.Errorf("registry request failed after retries: %w", err)
			}
			if err := wait(ctx, attempt); err != nil {
				return nil, err
			}
			continue
		}
		if response.StatusCode == http.StatusUnauthorized && token != "" && !refreshedToken {
			challenge, parseErr := auth.ParseChallenge(response.Header.Get("WWW-Authenticate"))
			response.Body.Close()
			if parseErr != nil {
				return nil, fmt.Errorf("registry rejected bearer token: %w", parseErr)
			}
			if challenge.Scope == "" {
				challenge.Scope = scope
			}
			if invalidator, ok := registry.tokens.(tokenInvalidator); ok {
				invalidator.Invalidate(challenge)
			}
			token = ""
			refreshedToken = true
			attempt--
			continue
		}
		if response.StatusCode == http.StatusUnauthorized && token == "" && registry.tokens != nil {
			challenge, parseErr := auth.ParseChallenge(response.Header.Get("WWW-Authenticate"))
			response.Body.Close()
			if parseErr != nil {
				return nil, fmt.Errorf("registry authentication challenge: %w", parseErr)
			}
			if challenge.Scope == "" {
				challenge.Scope = scope
			}
			origin, originErr := url.Parse(endpoint)
			realm, realmErr := url.Parse(challenge.Realm)
			if originErr != nil || realmErr != nil {
				return nil, fmt.Errorf("parse registry authentication URL")
			}
			if origin.Scheme == "https" && realm.Scheme != "https" {
				return nil, fmt.Errorf("refusing insecure bearer-token realm %q for HTTPS registry", challenge.Realm)
			}
			registry.logger.Debug("requesting registry bearer token", "realm", challenge.Realm, "service", challenge.Service, "scope", challenge.Scope)
			token, err = registry.tokens.Token(ctx, challenge)
			if err != nil {
				return nil, fmt.Errorf("retrieve registry bearer token: %w", err)
			}
			attempt--
			continue
		}
		if transient(response.StatusCode) {
			response.Body.Close()
			if attempt+1 == registry.retries {
				return nil, fmt.Errorf("registry returned transient HTTP %s after retries", response.Status)
			}
			if retryAfter := response.Header.Get("Retry-After"); retryAfter != "" {
				if delay, err := time.ParseDuration(retryAfter + "s"); err == nil {
					if err := waitDuration(ctx, delay); err != nil {
						return nil, err
					}
					continue
				}
			}
			if err := wait(ctx, attempt); err != nil {
				return nil, err
			}
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			response.Body.Close()
			return nil, fmt.Errorf("registry returned HTTP %s: %s", response.Status, strings.TrimSpace(string(detail)))
		}
		return response, nil
	}
	return nil, fmt.Errorf("registry request attempts exhausted")
}

func transient(code int) bool {
	return code == 408 || code == 429 || code == 500 || code == 502 || code == 503 || code == 504
}
func wait(ctx context.Context, attempt int) error {
	delay := time.Duration(1<<attempt) * 200 * time.Millisecond
	return waitDuration(ctx, delay)
}
func waitDuration(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("registry retry canceled: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}
