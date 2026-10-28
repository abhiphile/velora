// Package validator exposes the descriptor checks used by registry downloads.
package validator

import (
	"io"

	image "github.com/vellora/vellora/internal/image"
	imagevalidator "github.com/vellora/vellora/internal/image/validator"
)

func ValidateDescriptor(value image.Descriptor) error {
	return imagevalidator.ValidateDescriptor(value)
}
func VerifyReader(reader io.Reader, value image.Descriptor) error {
	return imagevalidator.VerifyReader(reader, value)
}
func VerifyFile(path string, value image.Descriptor) error {
	return imagevalidator.VerifyFile(path, value)
}
