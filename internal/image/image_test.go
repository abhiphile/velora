package image

import "testing"

func TestReferenceCanRepresentTaggedImage(t *testing.T) {
	reference := Reference{Name: "alpine", Tag: "latest"}
	if reference.Name != "alpine" || reference.Tag != "latest" {
		t.Fatalf("unexpected reference: %+v", reference)
	}
}
