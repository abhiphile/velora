package client

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/vellora/vellora/internal/image/registry/auth"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (fn transportFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestPingBearerChallengeFlow(t *testing.T) {
	tokenCalls, authorizedCalls := 0, 0
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasPrefix(request.URL.Host, "auth.") {
			tokenCalls++
			return response(request, 200, `{"token":"valid"}`, nil), nil
		}
		if request.Header.Get("Authorization") == "Bearer valid" {
			authorizedCalls++
			return response(request, 200, "", nil), nil
		}
		return response(request, 401, "", http.Header{"Www-Authenticate": {`Bearer realm="https://auth.test/token",service="test",scope="repository:alpine:pull"`}}), nil
	})
	httpClient := &http.Client{Transport: transport}
	tokens := auth.NewBearerProvider(httpClient, nil)
	registry, err := New(httpClient, tokens, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Ping(context.Background(), "https://registry.test", "repository:alpine:pull"); err != nil {
		t.Fatal(err)
	}
	if tokenCalls != 1 || authorizedCalls != 1 {
		t.Fatalf("token calls=%d authorized requests=%d", tokenCalls, authorizedCalls)
	}
}

func response(request *http.Request, status int, body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Body: io.NopCloser(strings.NewReader(body)), Header: headers, Request: request}
}
