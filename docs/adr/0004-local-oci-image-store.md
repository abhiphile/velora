# ADR 0004: Store local OCI images by content digest

## Status

Accepted for local OCI image support.

## Context

OCI layouts contain content-addressed blobs and location-addressed references. Vellora needs local listing, tag resolution, inspection, and deletion without contacting a registry. Image bytes should be shared between references and verified before use.

## Decision

Import OCI layout blobs into `<image_path>/blobs/sha256`, write validated summaries under `metadata`, store tag-to-manifest records under `refs`, and reserve `cache` for derived data. The local store supports SHA-256, OCI image manifest/config/index media types, and local layout version 1.0.0. Import validates descriptor size and digest before atomically installing a blob. Deleting a tag removes its ref and unreferenced metadata but retains blobs because content may be shared; garbage collection is deferred.

## Consequences

- Tags are mutable references; manifests/config/layer content remains addressed by digest.
- Multiple tags may point to one manifest and share all content blobs.
- Store import is offline and does not unpack layers into OverlayFS rootfs directories.
- Digest verification establishes byte integrity, not publisher authenticity.
- DiffID syntax/order is checked, but recomputing DiffID requires decompression and is deferred until layer unpacking.
- Metadata and refs are managed locally; persistent crash-safe transactions and garbage collection remain future work.

## Alternatives considered

- Treating an OCI layout directory itself as the runtime store would couple import/source paths to Vellora's metadata and lifecycle.
- Copying each tag's contents separately would duplicate shared config and layers.
- Downloading missing blobs would cross into OCI Distribution, explicitly outside this milestone.
