// Package reference parses local OCI image references.
package reference

import (
	"fmt"
	"regexp"
	"strings"

	image "github.com/vellora/vellora/internal/image"
	"github.com/vellora/vellora/internal/image/validator"
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)
var tagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// Parser implements the image reference parser interface.
type Parser struct{}

// NewParser returns a stateless parser for local image names.
func NewParser() Parser { return Parser{} }

// Parse validates a name[:tag][@sha256:digest] reference.
func (Parser) Parse(value string) (image.Reference, error) {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\\\x00\t\n ") {
		return image.Reference{}, fmt.Errorf("invalid image reference %q", value)
	}
	if strings.Count(value, "@") > 1 {
		return image.Reference{}, fmt.Errorf("image reference has multiple digest separators")
	}
	nameAndTag, digest, hasDigest := strings.Cut(value, "@")
	if hasDigest {
		if err := validator.ValidateDigest(digest); err != nil {
			return image.Reference{}, fmt.Errorf("parse image reference digest: %w", err)
		}
	}
	name := nameAndTag
	tag := ""
	lastSlash := strings.LastIndex(nameAndTag, "/")
	lastColon := strings.LastIndex(nameAndTag, ":")
	if lastColon > lastSlash {
		name, tag = nameAndTag[:lastColon], nameAndTag[lastColon+1:]
		if !tagPattern.MatchString(tag) {
			return image.Reference{}, fmt.Errorf("invalid image tag %q", tag)
		}
	}
	if !validName(name) {
		return image.Reference{}, fmt.Errorf("invalid image name %q", name)
	}
	if tag == "" && !hasDigest {
		tag = "latest"
	}
	return image.Reference{Name: name, Tag: tag, Digest: digest}, nil
}

func validName(value string) bool {
	if !namePattern.MatchString(value) || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, "//") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "." || component == ".." || component == "" {
			return false
		}
	}
	return true
}

var _ image.ReferenceParser = Parser{}
