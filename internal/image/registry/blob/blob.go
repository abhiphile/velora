// Package blob builds repository-scoped Distribution blob endpoints.
package blob

import (
	"fmt"
	"net/url"
	"strings"
)

func Endpoint(base, repository, digest string) (string, error) {
	if !validRepository(repository) {
		return "", fmt.Errorf("invalid registry repository %q", repository)
	}
	parts := strings.Split(repository, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.TrimSuffix(base, "/") + "/v2/" + strings.Join(parts, "/") + "/blobs/" + url.PathEscape(digest), nil
}
func ManifestEndpoint(base, repository, reference string) (string, error) {
	if !validRepository(repository) {
		return "", fmt.Errorf("invalid registry repository %q", repository)
	}
	parts := strings.Split(repository, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.TrimSuffix(base, "/") + "/v2/" + strings.Join(parts, "/") + "/manifests/" + url.PathEscape(reference), nil
}

func validRepository(repository string) bool {
	if strings.TrimSpace(repository) == "" || strings.HasPrefix(repository, "/") || strings.HasSuffix(repository, "/") {
		return false
	}
	for _, component := range strings.Split(repository, "/") {
		if component == "" || component == "." || component == ".." || component != strings.ToLower(component) || strings.ContainsAny(component, ":@\\\x00") {
			return false
		}
	}
	return true
}
