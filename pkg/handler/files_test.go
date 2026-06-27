package handler

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestUnitFilesSendChannelAllowlist verifies the files_send channel gate (Issue
// 11). files_send has no allowlist of its own: it reuses SLACK_MCP_ADD_MESSAGE_TOOL
// as the single source of truth and fails closed when that is empty. The gate runs
// before any apiProvider use, so these cases need no Slack client: a denied channel
// returns the allowlist error; an allowed channel passes the gate and then fails
// the later "filename is required" check, which proves the gate let it through.
func TestUnitFilesSendChannelAllowlist(t *testing.T) {
	fh := NewFilesHandler(nil, zap.NewNop())

	newReq := func(channelID string, extra map[string]any) mcp.CallToolRequest {
		req := mcp.CallToolRequest{}
		req.Params.Name = "files_send"
		args := map[string]any{"channel_id": channelID}
		for k, v := range extra {
			args[k] = v
		}
		req.Params.Arguments = args
		return req
	}

	t.Run("denied channel rejected", func(t *testing.T) {
		t.Setenv("SLACK_MCP_ADD_MESSAGE_TOOL", "C_ALLOWED,D_ALLOWED")
		req := newReq("C_DENIED", map[string]any{"filename": "x.txt", "content": "hi"})
		res, err := fh.FilesSendHandler(context.Background(), req)
		require.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "not allowed for files_send")
	})

	t.Run("allowed channel passes the gate", func(t *testing.T) {
		t.Setenv("SLACK_MCP_ADD_MESSAGE_TOOL", "C_ALLOWED,D_ALLOWED")
		// no input mode supplied on purpose: the request clears the allowlist
		// gate and then fails the input-required check, confirming the gate did
		// not block it.
		req := newReq("C_ALLOWED", nil)
		res, err := fh.FilesSendHandler(context.Background(), req)
		require.Error(t, err)
		assert.Nil(t, res)
		assert.NotContains(t, err.Error(), "not allowed for files_send")
		assert.Contains(t, err.Error(), "one of content, content_base64, or file_path is required")
	})

	t.Run("add_message true allows all channels", func(t *testing.T) {
		t.Setenv("SLACK_MCP_ADD_MESSAGE_TOOL", "true")
		req := newReq("C_ANYTHING", nil)
		_, err := fh.FilesSendHandler(context.Background(), req)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "not allowed for files_send")
		assert.Contains(t, err.Error(), "one of content, content_base64, or file_path is required")
	})

	t.Run("negation excludes the listed channel", func(t *testing.T) {
		t.Setenv("SLACK_MCP_ADD_MESSAGE_TOOL", "!C_BLOCKED")

		denied := newReq("C_BLOCKED", map[string]any{"filename": "x.txt", "content": "hi"})
		_, err := fh.FilesSendHandler(context.Background(), denied)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed for files_send")

		allowed := newReq("C_OTHER", nil)
		_, err = fh.FilesSendHandler(context.Background(), allowed)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "not allowed for files_send")
		assert.Contains(t, err.Error(), "one of content, content_base64, or file_path is required")
	})

	t.Run("fail closed when add_message allowlist is empty", func(t *testing.T) {
		// files_send shares the add_message allowlist; with it unset, every channel
		// must be denied rather than silently treated as "all allowed".
		t.Setenv("SLACK_MCP_ADD_MESSAGE_TOOL", "")
		req := newReq("C_ANYTHING", map[string]any{"filename": "x.txt", "content": "hi"})
		_, err := fh.FilesSendHandler(context.Background(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed for files_send")
	})

	t.Run("empty channel id rejected before allowlist", func(t *testing.T) {
		t.Setenv("SLACK_MCP_ADD_MESSAGE_TOOL", "true")
		req := newReq("", nil)
		_, err := fh.FilesSendHandler(context.Background(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "channel_id is required")
	})
}

// TestUnitOpenFileWithinRoot covers the file_path security guard: a file is read
// only when it is contained in SLACK_MCP_FILES_PATH_ROOT. Containment is enforced
// by os.Root at open time, so every escape vector (absolute path outside, ".."
// traversal, a symlink pointing out, /proc, an unset root) must fail closed and
// open nothing - in particular the server's own /proc/self/environ, which holds
// the Slack tokens. A directory target is rejected as non-regular.
func TestUnitOpenFileWithinRoot(t *testing.T) {
	fh := NewFilesHandler(nil, zap.NewNop())

	root := t.TempDir()
	// Resolve the root's own symlinks so the env value matches the canonical path
	// (e.g. macOS /var -> /private/var); on Linux this is usually a no-op.
	rootReal, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)

	insidePath := filepath.Join(rootReal, "photo.jpg")
	require.NoError(t, os.WriteFile(insidePath, []byte("img"), 0o600))

	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "secret.txt")
	require.NoError(t, os.WriteFile(outsidePath, []byte("secret"), 0o600))

	openContent := func(t *testing.T, p string) (string, int, error) {
		f, size, err := fh.openFileWithinRoot(p)
		if err != nil {
			return "", 0, err
		}
		defer f.Close()
		b, rerr := io.ReadAll(f)
		require.NoError(t, rerr)
		return string(b), size, nil
	}

	t.Run("absolute path inside root opens", func(t *testing.T) {
		t.Setenv("SLACK_MCP_FILES_PATH_ROOT", rootReal)
		content, size, err := openContent(t, insidePath)
		require.NoError(t, err)
		assert.Equal(t, "img", content)
		assert.Equal(t, 3, size)
	})

	t.Run("relative path inside root opens", func(t *testing.T) {
		t.Setenv("SLACK_MCP_FILES_PATH_ROOT", rootReal)
		content, size, err := openContent(t, "photo.jpg")
		require.NoError(t, err)
		assert.Equal(t, "img", content)
		assert.Equal(t, 3, size)
	})

	t.Run("absolute path outside root rejected", func(t *testing.T) {
		t.Setenv("SLACK_MCP_FILES_PATH_ROOT", rootReal)
		_, _, err := fh.openFileWithinRoot(outsidePath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside")
	})

	t.Run("dot-dot traversal rejected", func(t *testing.T) {
		t.Setenv("SLACK_MCP_FILES_PATH_ROOT", rootReal)
		rel, err := filepath.Rel(rootReal, outsidePath)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(rel, ".."+string(os.PathSeparator)),
			"expected a parent-traversal relative path, got %q", rel)
		_, _, err = fh.openFileWithinRoot(rel)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside")
	})

	t.Run("symlink escaping root rejected", func(t *testing.T) {
		t.Setenv("SLACK_MCP_FILES_PATH_ROOT", rootReal)
		link := filepath.Join(rootReal, "link-to-secret")
		require.NoError(t, os.Symlink(outsidePath, link))
		t.Cleanup(func() { _ = os.Remove(link) })
		_, _, err := fh.openFileWithinRoot(link)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside")
	})

	t.Run("proc self environ rejected", func(t *testing.T) {
		t.Setenv("SLACK_MCP_FILES_PATH_ROOT", rootReal)
		_, _, err := fh.openFileWithinRoot("/proc/self/environ")
		require.Error(t, err)
	})

	t.Run("directory rejected as non-regular", func(t *testing.T) {
		t.Setenv("SLACK_MCP_FILES_PATH_ROOT", rootReal)
		subdir := filepath.Join(rootReal, "subdir")
		require.NoError(t, os.Mkdir(subdir, 0o755))
		t.Cleanup(func() { _ = os.Remove(subdir) })
		_, _, err := fh.openFileWithinRoot(subdir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "regular")
	})

	t.Run("unset root fails closed", func(t *testing.T) {
		t.Setenv("SLACK_MCP_FILES_PATH_ROOT", "")
		_, _, err := fh.openFileWithinRoot(insidePath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SLACK_MCP_FILES_PATH_ROOT")
	})

	t.Run("nonexistent file rejected", func(t *testing.T) {
		t.Setenv("SLACK_MCP_FILES_PATH_ROOT", rootReal)
		_, _, err := fh.openFileWithinRoot(filepath.Join(rootReal, "nope.bin"))
		require.Error(t, err)
	})
}

// TestUnitBuildCaptionBlocks verifies the caption wiring: an empty caption sends
// no blocks (so the upload carries no comment), and a non-empty caption becomes
// a single rich_text block so the file message renders with the same formatting
// as conversations_add_message.
func TestUnitBuildCaptionBlocks(t *testing.T) {
	t.Run("empty caption yields no blocks", func(t *testing.T) {
		b := buildCaptionBlocks("")
		assert.Nil(t, b.BlockSet)
	})

	t.Run("non-empty caption yields one rich_text block", func(t *testing.T) {
		b := buildCaptionBlocks("**Latte** for <@U123>\n- one\n- two")
		require.Len(t, b.BlockSet, 1)
		_, ok := b.BlockSet[0].(*slack.RichTextBlock)
		assert.True(t, ok, "expected a *slack.RichTextBlock")
	})
}

// TestUnitFilesSendFilePathValidation drives file_path through the full handler
// for the cases that must fail before any Slack call. The handler is built with a
// nil apiProvider, so a panic here would mean the guard let the request reach the
// upload path.
func TestUnitFilesSendFilePathValidation(t *testing.T) {
	fh := NewFilesHandler(nil, zap.NewNop())
	newReq := func(args map[string]any) mcp.CallToolRequest {
		req := mcp.CallToolRequest{}
		req.Params.Name = "files_send"
		req.Params.Arguments = args
		return req
	}

	t.Run("file_path with content is mutually exclusive", func(t *testing.T) {
		t.Setenv("SLACK_MCP_ADD_MESSAGE_TOOL", "true")
		req := newReq(map[string]any{"channel_id": "C1", "file_path": "/uploads/x.jpg", "content": "hi"})
		_, err := fh.FilesSendHandler(context.Background(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	})

	t.Run("file_path with unset root fails closed", func(t *testing.T) {
		t.Setenv("SLACK_MCP_ADD_MESSAGE_TOOL", "true")
		t.Setenv("SLACK_MCP_FILES_PATH_ROOT", "")
		req := newReq(map[string]any{"channel_id": "C1", "file_path": "/uploads/x.jpg"})
		_, err := fh.FilesSendHandler(context.Background(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SLACK_MCP_FILES_PATH_ROOT")
	})

	t.Run("file_path outside root rejected before upload", func(t *testing.T) {
		t.Setenv("SLACK_MCP_ADD_MESSAGE_TOOL", "true")
		t.Setenv("SLACK_MCP_FILES_PATH_ROOT", t.TempDir())
		req := newReq(map[string]any{"channel_id": "C1", "file_path": "/proc/self/environ"})
		_, err := fh.FilesSendHandler(context.Background(), req)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "uploaded")
	})
}
