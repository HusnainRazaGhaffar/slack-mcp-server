//go:build unix

package handler

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestUnitOpenFileWithinRootFIFO verifies that a FIFO staged inside the root is
// rejected as non-regular and, crucially, does not hang the request. openFileWithinRoot
// stats before opening precisely because opening a writer-less FIFO blocks forever.
// The subtest runs under a timeout so a regression (open before the type check)
// surfaces as a failure instead of a hung suite. unix-only: syscall.Mkfifo.
func TestUnitOpenFileWithinRootFIFO(t *testing.T) {
	fh := NewFilesHandler(nil, zap.NewNop())
	root := t.TempDir()
	t.Setenv("SLACK_MCP_FILES_PATH_ROOT", root)

	fifo := filepath.Join(root, "pipe")
	require.NoError(t, syscall.Mkfifo(fifo, 0o644))

	done := make(chan error, 1)
	go func() {
		f, _, err := fh.openFileWithinRoot(fifo)
		if f != nil {
			f.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "regular")
	case <-time.After(3 * time.Second):
		t.Fatal("openFileWithinRoot hung on a FIFO (the regular-file stat check must precede the open)")
	}
}
