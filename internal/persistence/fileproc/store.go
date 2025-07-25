package fileproc

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/dagu-org/dagu/internal/digraph"
	"github.com/dagu-org/dagu/internal/models"
)

var _ models.ProcStore = (*Store)(nil)

// Store is a struct that implements the ProcStore interface.
type Store struct {
	baseDir    string
	staleTime  time.Duration
	procGroups sync.Map
}

// New creates a new instance of Store with the specified base directory.
func New(baseDir string) *Store {
	return &Store{
		baseDir:   baseDir,
		staleTime: time.Second * 45,
	}
}

// CountAlive implements models.ProcStore.
func (s *Store) CountAlive(ctx context.Context, name string) (int, error) {
	pgBaseDir := filepath.Join(s.baseDir, name)
	pg, _ := s.procGroups.LoadOrStore(name, NewProcGroup(pgBaseDir, name, s.staleTime))
	procGroup, ok := pg.(*ProcGroup)
	if !ok {
		return 0, fmt.Errorf("invalid type in procGroups map: expected *ProcGroup, got %T", pg)
	}
	return procGroup.Count(ctx)
}

// Acquire implements models.ProcStore.
func (s *Store) Acquire(ctx context.Context, dagRun digraph.DAGRunRef) (models.ProcHandle, error) {
	pgBaseDir := filepath.Join(s.baseDir, dagRun.Name)
	pg, _ := s.procGroups.LoadOrStore(dagRun.Name, NewProcGroup(pgBaseDir, dagRun.Name, s.staleTime))
	procGroup, ok := pg.(*ProcGroup)
	if !ok {
		return nil, fmt.Errorf("invalid type in procGroups map: expected *ProcGroup, got %T", pg)
	}
	proc, err := procGroup.Acquire(ctx, dagRun)
	if err != nil {
		return nil, err
	}
	if err := proc.startHeartbeat(ctx); err != nil {
		return nil, err
	}
	return proc, nil
}

// IsRunAlive implements models.ProcStore.
func (s *Store) IsRunAlive(ctx context.Context, dagRun digraph.DAGRunRef) (bool, error) {
	pgBaseDir := filepath.Join(s.baseDir, dagRun.Name)
	pg, _ := s.procGroups.LoadOrStore(dagRun.Name, NewProcGroup(pgBaseDir, dagRun.Name, s.staleTime))
	procGroup, ok := pg.(*ProcGroup)
	if !ok {
		return false, fmt.Errorf("invalid type in procGroups map: expected *ProcGroup, got %T", pg)
	}
	return procGroup.IsRunAlive(ctx, dagRun)
}
