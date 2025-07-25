package fileproc

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/dagu-org/dagu/internal/digraph"
	"github.com/dagu-org/dagu/internal/logger"
	"github.com/dagu-org/dagu/internal/models"
)

// ProcGroup is a struct that manages process files for a given DAG name.
type ProcGroup struct {
	name      string
	baseDir   string
	staleTime time.Duration
	mu        sync.Mutex
}

// procFilePrefix is the prefix for the proc files
const procFilePrefix = "proc_"

// procFileRegex is a regex pattern to match the proc file name format
var procFileRegex = regexp.MustCompile(`^proc_\d{8}_\d{6}Z_.*\.proc$`)

// NewProcGroup creates a new instance of a ProcGroup with the specified base directory and DAG name.
func NewProcGroup(baseDir, name string, staleTime time.Duration) *ProcGroup {
	return &ProcGroup{
		baseDir:   baseDir,
		name:      name,
		staleTime: staleTime,
	}
}

// Count retrieves the count of alive proc files for the specified DAG name.
func (pg *ProcGroup) Count(ctx context.Context) (int, error) {
	pg.mu.Lock()
	defer pg.mu.Unlock()

	// If directory does not exist, return 0
	if _, err := os.Stat(pg.baseDir); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}

	// Grep for all proc files in the directory
	files, err := filepath.Glob(filepath.Join(pg.baseDir, procFilePrefix+"*.proc"))
	if err != nil {
		return 0, err
	}

	aliveCount := 0
	for _, file := range files {
		if !procFileRegex.MatchString(filepath.Base(file)) {
			continue
		}
		// Check if the file is stale
		if !pg.isStale(ctx, file) {
			aliveCount++
			continue
		}
		// File is stale, remove it
		if err := os.Remove(file); err != nil {
			logger.Error(ctx, "failed to remove stale file %s: %v", file, err)
		}
		continue
	}

	return aliveCount, nil
}

// isStale checks if the proc file is stale based on its content (timestamp).
func (pg *ProcGroup) isStale(ctx context.Context, file string) bool {
	// Check if the file exists
	fileInfo, err := os.Stat(file)
	if err != nil {
		return true // File does not exist, consider it stale
	}

	if time.Since(fileInfo.ModTime()) < pg.staleTime {
		return false // File is not stale
	}

	// Check if the file is stale by checking its content (timestamp).
	data, err := os.ReadFile(file) // nolint:gosec
	if err != nil {
		logger.Warn(ctx, "failed to read file %s: %v", file, err)
		// If we can't read the file, consider it stale
		return true
	}

	// It is assumed that the first 8 bytes of the file contain a timestamp in seconds (unix time).
	if len(data) < 8 {
		logger.Warn(ctx, "file %s is too short (got %d bytes, expected at least 8), considering it stale", file, len(data))
		return true
	}

	// Parse the timestamp from the file
	unixTime := int64(binary.BigEndian.Uint64(data[:8])) // nolint:gosec

	// Validate the timestamp is reasonable (not in the future, not too old)
	now := time.Now()
	parsedTime := time.Unix(unixTime, 0)

	if parsedTime.After(now.Add(5 * time.Minute)) {
		logger.Warn(ctx, "proc file %s has timestamp in the future (%s), considering it stale", file, parsedTime)
		return true
	}

	duration := now.Sub(parsedTime)
	if duration < pg.staleTime {
		logger.Debug(ctx, "proc file %s is not stale, last heartbeat at %s (%v ago)", file, parsedTime, duration)
		return false
	}
	logger.Debug(ctx, "proc file %s is stale, last heartbeat at %s (%v ago, threshold: %v)", file, parsedTime, duration, pg.staleTime)
	return true
}

// GetProc retrieves a proc file for the specified dag-run reference.
// It returns a new Proc instance with the generated file name.
func (pg *ProcGroup) Acquire(_ context.Context, dagRun digraph.DAGRunRef) (*ProcHandle, error) {
	// Sanity check the dag-run reference
	if pg.name != dagRun.Name {
		return nil, fmt.Errorf("DAG name %s does not match proc file name %s", dagRun.Name, pg.name)
	}
	// Generate the proc file name
	fileName := pg.getFileName(models.NewUTC(time.Now()), dagRun)
	return NewProcHandler(fileName, models.ProcMeta{
		StartedAt: time.Now().Unix(),
	}), nil
}

// getFileName generates a proc file name based on the dag-run reference and the current time.
func (pg *ProcGroup) getFileName(t models.TimeInUTC, dagRun digraph.DAGRunRef) string {
	timestamp := t.Format(dateTimeFormatUTC)
	fileName := procFilePrefix + timestamp + "Z_" + dagRun.ID + ".proc"
	return filepath.Join(pg.baseDir, fileName)
}

// dateTimeFormat is the format used for the timestamp in the queue file name
const dateTimeFormatUTC = "20060102_150405"

// IsRunAlive checks if a specific DAG run has an alive process file.
func (pg *ProcGroup) IsRunAlive(ctx context.Context, dagRun digraph.DAGRunRef) (bool, error) {
	pg.mu.Lock()
	defer pg.mu.Unlock()

	// If directory does not exist, return false
	if _, err := os.Stat(pg.baseDir); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	// Look for proc files with the specific run ID
	pattern := filepath.Join(pg.baseDir, procFilePrefix+"*_"+dagRun.ID+".proc")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return false, err
	}

	// Check each matching file
	for _, file := range files {
		if !procFileRegex.MatchString(filepath.Base(file)) {
			continue
		}
		// Check if the file is stale
		if !pg.isStale(ctx, file) {
			return true, nil
		}
		// File is stale, remove it
		if err := os.Remove(file); err != nil {
			logger.Error(ctx, "failed to remove stale file %s: %v", file, err)
		}
	}

	return false, nil
}
