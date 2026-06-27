package handler

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
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
		// filename omitted on purpose: the request clears the allowlist gate and
		// then fails the filename check, confirming the gate did not block it.
		req := newReq("C_ALLOWED", nil)
		res, err := fh.FilesSendHandler(context.Background(), req)
		require.Error(t, err)
		assert.Nil(t, res)
		assert.NotContains(t, err.Error(), "not allowed for files_send")
		assert.Contains(t, err.Error(), "filename is required")
	})

	t.Run("add_message true allows all channels", func(t *testing.T) {
		t.Setenv("SLACK_MCP_ADD_MESSAGE_TOOL", "true")
		req := newReq("C_ANYTHING", nil)
		_, err := fh.FilesSendHandler(context.Background(), req)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "not allowed for files_send")
		assert.Contains(t, err.Error(), "filename is required")
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
		assert.Contains(t, err.Error(), "filename is required")
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
