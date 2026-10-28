// Package snapshot manages persistent, parented OverlayFS snapshots.
package snapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusActive    Status = "active"
	StatusCommitted Status = "committed"
)

// Metadata is the durable description of one snapshot. ReferenceCount includes children and mounts.
type Metadata struct {
	ID             string    `json:"id"`
	Parent         string    `json:"parent,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	Status         Status    `json:"status"`
	ReferenceCount int       `json:"reference_count"`
	Mounted        bool      `json:"mounted"`
	Paths          Paths     `json:"paths"`
	ContainerID    string    `json:"container_id,omitempty"`
}

type Paths struct {
	Root   string `json:"root"`
	Upper  string `json:"upper"`
	Work   string `json:"work"`
	Merged string `json:"merged"`
}

type Snapshotter interface {
	Prepare(context.Context, string, string) (Metadata, error)
	Commit(context.Context, string) (Metadata, error)
	Mount(context.Context, string) (string, error)
	Unmount(context.Context, string) error
	Delete(context.Context, string) error
	List(context.Context) ([]Metadata, error)
	Inspect(context.Context, string) (Metadata, error)
	GarbageCollect(context.Context) ([]string, error)
}

type SnapshotStore interface {
	Create(Metadata) error
	Get(string) (Metadata, error)
	Update(Metadata) error
	Delete(string) error
	List() ([]Metadata, error)
}
type SnapshotMetadataStore interface{ SnapshotStore }
type SnapshotCleaner interface{ RemoveTree(string) error }
type MountManager interface {
	MountOverlay([]string, string, string, string) error
	Unmount(string) error
}

type Manager struct {
	root    string
	store   SnapshotStore
	cleaner SnapshotCleaner
	mounts  MountManager
	logger  *slog.Logger
	mu      sync.Mutex
}

func New(root string, logger *slog.Logger) (Snapshotter, error) {
	if root == "" || logger == nil {
		return nil, fmt.Errorf("snapshot root and logger are required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve snapshot root: %w", err)
	}
	return newManager(absolute, newFileStore(filepath.Join(absolute, "snapshots")), osCleaner{}, platformMounts(), logger)
}

func newManager(root string, store SnapshotStore, cleaner SnapshotCleaner, mounts MountManager, logger *slog.Logger) (*Manager, error) {
	if root == "" || store == nil || cleaner == nil || mounts == nil || logger == nil {
		return nil, fmt.Errorf("snapshot manager dependencies are required")
	}
	if err := os.MkdirAll(filepath.Join(root, "snapshots"), 0700); err != nil {
		return nil, fmt.Errorf("create snapshots directory: %w", err)
	}
	return &Manager{root: root, store: store, cleaner: cleaner, mounts: mounts, logger: logger}, nil
}

// Prepare creates a writable snapshot. parent is an absolute immutable layer path or committed snapshot ID.
func (m *Manager) Prepare(ctx context.Context, id, parent string) (Metadata, error) {
	if err := ctx.Err(); err != nil {
		return Metadata{}, fmt.Errorf("prepare snapshot: %w", err)
	}
	if !validID(id) || parent == "" {
		return Metadata{}, fmt.Errorf("snapshot ID and parent are required; ID must be a simple path component")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.store.Get(id); err == nil {
		return Metadata{}, fmt.Errorf("snapshot %q already exists", id)
	}
	if _, err := m.resolveParent(parent); err != nil {
		return Metadata{}, fmt.Errorf("resolve snapshot parent %q: %w", parent, err)
	}
	paths := snapshotPaths(m.root, id)
	if err := os.Mkdir(paths.Root, 0700); err != nil {
		return Metadata{}, fmt.Errorf("create snapshot directory: %w", err)
	}
	rollback := func(cause error) (Metadata, error) {
		if cleanupErr := m.cleaner.RemoveTree(paths.Root); cleanupErr != nil {
			cause = fmt.Errorf("%w; rollback snapshot directory: %v", cause, cleanupErr)
		}
		return Metadata{}, cause
	}
	for _, dir := range []string{paths.Upper, paths.Work, paths.Merged} {
		if err := os.Mkdir(dir, 0700); err != nil {
			return rollback(fmt.Errorf("create snapshot path %q: %w", dir, err))
		}
	}
	if parentMeta, err := m.store.Get(parent); err == nil {
		parentMeta.ReferenceCount++
		if err := m.store.Update(parentMeta); err != nil {
			return rollback(fmt.Errorf("retain parent snapshot: %w", err))
		}
	}
	metadata := Metadata{ID: id, Parent: parent, CreatedAt: time.Now().UTC(), Status: StatusActive, Paths: paths}
	if err := m.store.Create(metadata); err != nil {
		if parentMeta, getErr := m.store.Get(parent); getErr == nil {
			parentMeta.ReferenceCount--
			_ = m.store.Update(parentMeta)
		}
		return rollback(fmt.Errorf("persist snapshot metadata: %w", err))
	}
	m.logger.Info("created snapshot", "id", id, "parent", parent, "upper", paths.Upper)
	return metadata, nil
}

func (m *Manager) Commit(ctx context.Context, id string) (Metadata, error) {
	if err := ctx.Err(); err != nil {
		return Metadata{}, fmt.Errorf("commit snapshot: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	item, err := m.store.Get(id)
	if err != nil {
		return Metadata{}, fmt.Errorf("load snapshot %q: %w", id, err)
	}
	if item.Status != StatusActive {
		return Metadata{}, fmt.Errorf("snapshot %q is not active", id)
	}
	if item.Mounted {
		return Metadata{}, fmt.Errorf("snapshot %q must be unmounted before commit", id)
	}
	item.Status = StatusCommitted
	if err := m.store.Update(item); err != nil {
		return Metadata{}, fmt.Errorf("persist committed snapshot: %w", err)
	}
	m.logger.Info("committed snapshot", "id", id)
	return item, nil
}

func (m *Manager) Mount(ctx context.Context, id string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("mount snapshot: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	item, err := m.store.Get(id)
	if err != nil {
		return "", fmt.Errorf("load snapshot %q: %w", id, err)
	}
	if item.Status != StatusActive {
		return "", fmt.Errorf("only active snapshots can be mounted")
	}
	if item.Mounted {
		return "", fmt.Errorf("snapshot %q is already mounted", id)
	}
	lowers, err := m.lowerChain(item.Parent)
	if err != nil {
		return "", fmt.Errorf("resolve snapshot lower chain: %w", err)
	}
	if err := m.mounts.MountOverlay(lowers, item.Paths.Upper, item.Paths.Work, item.Paths.Merged); err != nil {
		return "", fmt.Errorf("mount snapshot %q: %w", id, err)
	}
	item.ReferenceCount++
	item.Mounted = true
	if err := m.store.Update(item); err != nil {
		_ = m.mounts.Unmount(item.Paths.Merged)
		return "", fmt.Errorf("persist mounted snapshot: %w", err)
	}
	m.logger.Info("mounted snapshot", "id", id, "merged", item.Paths.Merged)
	return item.Paths.Merged, nil
}

func (m *Manager) Unmount(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("unmount snapshot: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	item, err := m.store.Get(id)
	if err != nil {
		return fmt.Errorf("load snapshot %q: %w", id, err)
	}
	if item.ReferenceCount == 0 {
		return fmt.Errorf("snapshot %q has no mount reference", id)
	}
	if !item.Mounted {
		return fmt.Errorf("snapshot %q is not mounted", id)
	}
	if err := m.mounts.Unmount(item.Paths.Merged); err != nil {
		return fmt.Errorf("unmount snapshot %q: %w", id, err)
	}
	item.ReferenceCount--
	item.Mounted = false
	if err := m.store.Update(item); err != nil {
		return fmt.Errorf("persist unmounted snapshot: %w", err)
	}
	m.logger.Info("unmounted snapshot", "id", id)
	return nil
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete snapshot: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	item, err := m.store.Get(id)
	if err != nil {
		return fmt.Errorf("load snapshot %q: %w", id, err)
	}
	if item.ReferenceCount != 0 {
		return fmt.Errorf("snapshot %q is referenced %d time(s)", id, item.ReferenceCount)
	}
	children, err := m.store.List()
	if err != nil {
		return fmt.Errorf("list child snapshots: %w", err)
	}
	for _, child := range children {
		if child.Parent == id {
			return fmt.Errorf("snapshot %q has child %q", id, child.ID)
		}
	}
	if err := m.cleaner.RemoveTree(item.Paths.Root); err != nil {
		return fmt.Errorf("remove snapshot filesystem after metadata: %w", err)
	}
	if err := m.store.Delete(id); err != nil {
		return fmt.Errorf("remove snapshot metadata after filesystem: %w", err)
	}
	if parent, err := m.store.Get(item.Parent); err == nil && parent.ReferenceCount > 0 {
		parent.ReferenceCount--
		if err := m.store.Update(parent); err != nil {
			return fmt.Errorf("release parent snapshot reference: %w", err)
		}
	}
	m.logger.Info("deleted snapshot", "id", id)
	return nil
}

func (m *Manager) List(ctx context.Context) ([]Metadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	return m.store.List()
}
func (m *Manager) Inspect(ctx context.Context, id string) (Metadata, error) {
	if err := ctx.Err(); err != nil {
		return Metadata{}, fmt.Errorf("inspect snapshot: %w", err)
	}
	return m.store.Get(id)
}

func (m *Manager) GarbageCollect(ctx context.Context) ([]string, error) {
	var removed []string
	for {
		items, err := m.List(ctx)
		if err != nil {
			return removed, err
		}
		parents := make(map[string]bool, len(items))
		for _, item := range items {
			if item.Parent != "" {
				parents[item.Parent] = true
			}
		}
		progress := false
		for _, item := range items {
			if item.ReferenceCount != 0 || item.Mounted || parents[item.ID] {
				continue
			}
			if err := m.Delete(ctx, item.ID); err != nil {
				return removed, fmt.Errorf("garbage collect snapshot %q: %w", item.ID, err)
			}
			removed = append(removed, item.ID)
			progress = true
		}
		if !progress {
			return removed, nil
		}
	}
}

func (m *Manager) resolveParent(parent string) (string, error) {
	if meta, err := m.store.Get(parent); err == nil {
		if meta.Status != StatusCommitted {
			return "", fmt.Errorf("parent snapshot %q is not committed", parent)
		}
		return filepath.Join(meta.Paths.Root, "upper"), nil
	}
	if !filepath.IsAbs(parent) {
		return "", fmt.Errorf("base layer parent must be an absolute path or committed snapshot ID")
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("parent layer is not a directory")
	}
	return resolved, nil
}

func (m *Manager) lowerChain(parent string) ([]string, error) {
	meta, err := m.store.Get(parent)
	if err != nil {
		resolved, resolveErr := m.resolveParent(parent)
		if resolveErr != nil {
			return nil, resolveErr
		}
		return []string{resolved}, nil
	}
	var layers []string
	for depth := 0; depth < 128; depth++ {
		if meta.Status != StatusCommitted {
			return nil, fmt.Errorf("parent %q is not committed", meta.ID)
		}
		layers = append(layers, meta.Paths.Upper)
		next, err := m.store.Get(meta.Parent)
		if err != nil {
			base, resolveErr := m.resolveParent(meta.Parent)
			if resolveErr != nil {
				return nil, resolveErr
			}
			layers = append(layers, base)
			return layers, nil
		}
		meta = next
	}
	return nil, fmt.Errorf("snapshot parent chain exceeds 128 entries")
}

func snapshotPaths(root, id string) Paths {
	base := filepath.Join(root, "snapshots", id)
	return Paths{Root: base, Upper: filepath.Join(base, "upper"), Work: filepath.Join(base, "work"), Merged: filepath.Join(base, "merged")}
}
func validID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id && !strings.ContainsAny(id, "/\\\x00")
}

type fileStore struct {
	root string
	mu   sync.Mutex
}

func newFileStore(root string) *fileStore  { return &fileStore{root: root} }
func (s *fileStore) path(id string) string { return filepath.Join(s.root, id, "metadata.json") }
func (s *fileStore) Create(v Metadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.read(v.ID); err == nil {
		return fmt.Errorf("snapshot exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	return s.write(v)
}
func (s *fileStore) Get(id string) (Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(id)
}
func (s *fileStore) Update(v Metadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.read(v.ID); err != nil {
		return err
	}
	return s.write(v)
}
func (s *fileStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path(id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
func (s *fileStore) List() ([]Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	out := make([]Metadata, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || !validID(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.root, e.Name(), "metadata.json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var v Metadata
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, fmt.Errorf("decode snapshot metadata %q: %w", e.Name(), err)
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *fileStore) read(id string) (Metadata, error) {
	if !validID(id) {
		return Metadata{}, fmt.Errorf("invalid snapshot ID")
	}
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		return Metadata{}, err
	}
	var v Metadata
	if err := json.Unmarshal(b, &v); err != nil {
		return Metadata{}, err
	}
	if v.ID != id {
		return Metadata{}, fmt.Errorf("snapshot metadata ID mismatch")
	}
	return v, nil
}
func (s *fileStore) write(v Metadata) error {
	path := s.path(v.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".metadata-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

type osCleaner struct{}

func (osCleaner) RemoveTree(path string) error { return os.RemoveAll(path) }

var _ Snapshotter = (*Manager)(nil)
var _ SnapshotMetadataStore = (*fileStore)(nil)
