package auth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestParseBearerChallenge(t *testing.T) {
	got, err := ParseChallenge(`Bearer realm="https://auth.example/token",service="registry.example",scope="repository:team/app:pull"`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Realm != "https://auth.example/token" || got.Service != "registry.example" || got.Scope != "repository:team/app:pull" {
		t.Fatalf("challenge=%+v", got)
	}
}

func TestBearerProviderFetchesAndCachesToken(t *testing.T) {
	calls := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Query().Get("scope") != "repository:team/app:pull" {
			t.Errorf("scope=%q", request.URL.Query().Get("scope"))
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"token":"secret","expires_in":3600}`)), Header: make(http.Header), Request: request}, nil
	})}
	provider := NewBearerProvider(httpClient, nil)
	challenge := Challenge{Realm: "https://auth.example/token", Service: "registry.test", Scope: "repository:team/app:pull"}
	for i := 0; i < 2; i++ {
		token, err := provider.Token(context.Background(), challenge)
		if err != nil || token != "secret" {
			t.Fatalf("token=%q err=%v", token, err)
		}
	}
	if calls != 1 {
		t.Fatalf("token endpoint calls=%d", calls)
	}
}
