package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/korotovsky/slack-mcp-server/pkg/text"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

const maxFileSize = 50 * 1024 * 1024 // 50MB

type FilesHandler struct {
	apiProvider *provider.ApiProvider
	logger      *zap.Logger
}

func NewFilesHandler(apiProvider *provider.ApiProvider, logger *zap.Logger) *FilesHandler {
	return &FilesHandler{
		apiProvider: apiProvider,
		logger:      logger,
	}
}

// FilesSendHandler uploads a file to a Slack channel or thread. The file bytes
// come from exactly one of three inputs: content (UTF-8 text), content_base64
// (base64-encoded binary supplied inline), or file_path (a path the server reads
// off disk). file_path is the only input that works for real binary files driven
// by an LLM, which cannot reproduce large base64 faithfully; it is guarded by
// SLACK_MCP_FILES_PATH_ROOT so the server only ever reads files under an
// operator-chosen directory. A non-empty initial_comment is rendered as a
// rich_text block, the same way conversations_add_message renders message text.
func (fh *FilesHandler) FilesSendHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	channelID := request.GetString("channel_id", "")
	if channelID == "" {
		return nil, errors.New("channel_id is required")
	}
	if !isChannelAllowedForFiles(channelID) {
		return nil, fmt.Errorf("channel %s is not allowed for files_send; uploads are limited to the SLACK_MCP_ADD_MESSAGE_TOOL channels (set that variable to permit it)", channelID)
	}

	content := request.GetString("content", "")
	contentBase64 := request.GetString("content_base64", "")
	filePath := request.GetString("file_path", "")
	filename := request.GetString("filename", "")
	threadTs := request.GetString("thread_ts", "")
	initialComment := request.GetString("initial_comment", "")

	// Exactly one input mode must be supplied.
	modes := 0
	for _, set := range []bool{content != "", contentBase64 != "", filePath != ""} {
		if set {
			modes++
		}
	}
	if modes == 0 {
		return nil, errors.New("one of content, content_base64, or file_path is required")
	}
	if modes > 1 {
		return nil, errors.New("content, content_base64, and file_path are mutually exclusive")
	}

	var reader io.Reader
	var fileSize int

	switch {
	case filePath != "":
		f, size, err := fh.openFileWithinRoot(filePath)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		reader = f
		fileSize = size
		if filename == "" {
			filename = filepath.Base(filePath)
		}
	case contentBase64 != "":
		data, err := base64.StdEncoding.DecodeString(contentBase64)
		if err != nil {
			return nil, fmt.Errorf("failed to decode content_base64: %w", err)
		}
		if len(data) > maxFileSize {
			return nil, fmt.Errorf("file too large (%d bytes, max %d bytes)", len(data), maxFileSize)
		}
		reader = bytes.NewReader(data)
		fileSize = len(data)
	default: // content
		data := []byte(content)
		if len(data) > maxFileSize {
			return nil, fmt.Errorf("file too large (%d bytes, max %d bytes)", len(data), maxFileSize)
		}
		reader = bytes.NewReader(data)
		fileSize = len(data)
	}

	if filename == "" {
		return nil, errors.New("filename is required")
	}

	params := slack.UploadFileParameters{
		Filename:        filename,
		FileSize:        fileSize,
		Reader:          reader,
		Channel:         channelID,
		ThreadTimestamp: threadTs,
		Blocks:          buildCaptionBlocks(initialComment),
	}

	summary, err := fh.apiProvider.Slack().UploadFileContext(ctx, params)
	if err != nil {
		fh.logger.Error("UploadFileContext failed", zap.Error(err))
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}

	result := fmt.Sprintf("File uploaded successfully.\nFile ID: %s\nTitle: %s", summary.ID, summary.Title)
	return mcp.NewToolResultText(result), nil
}

// buildCaptionBlocks renders a non-empty caption as a single rich_text block,
// matching how conversations_add_message renders message text (native lists,
// real mention tokens, underscore-safe). An empty caption yields a zero Blocks
// value (nil BlockSet), which slack-go treats as "no blocks". InitialComment is
// deliberately left empty by the caller: slack-go sends blocks only when
// InitialComment is empty, so the trade-off is that a formatted caption carries
// no separate plain-text notification fallback (Slack derives the notification
// from the blocks).
func buildCaptionBlocks(initialComment string) slack.Blocks {
	if initialComment == "" {
		return slack.Blocks{}
	}
	rtBlock := text.ConvertMarkdownToRichTextBlock(initialComment)
	return slack.Blocks{BlockSet: []slack.Block{rtBlock}}
}

// openFileWithinRoot opens filePath for reading, confined to the directory named
// by SLACK_MCP_FILES_PATH_ROOT, and returns the open file (the caller closes it)
// with its size. It fails closed: an unset root disables the feature, and any
// path that escapes the root - via "..", an absolute path such as
// /proc/self/environ, or a symlink whose target leaves the root - is rejected.
//
// Containment is enforced by os.Root, which validates every path component at
// open time using directory file descriptors; there is no resolve-then-open gap,
// so a symlink swapped in after a check cannot redirect the read outside the
// root. os.Root rejects an absolute name, so an absolute file_path that points
// under the root is first rewritten to a root-relative name (one that points
// elsewhere becomes a "../" path that os.Root then rejects).
//
// A Root.Stat precedes the open to reject a non-regular target (FIFO, device,
// socket): stat never blocks, but opening a writer-less FIFO would hang the
// request. The only residual is a regular-to-FIFO swap between that stat and the
// open, which requires write access to the mounted root; the root is mounted
// read-only, and host write access is outside this tool's threat model (the
// attacker-influenced input is the file_path string, fully contained here).
//
// Caller-facing errors are deliberately generic so they do not leak absolute
// container paths or an existence oracle; the specific cause is logged.
func (fh *FilesHandler) openFileWithinRoot(filePath string) (*os.File, int, error) {
	root := os.Getenv("SLACK_MCP_FILES_PATH_ROOT")
	if root == "" {
		return nil, 0, errors.New("file_path is disabled: set SLACK_MCP_FILES_PATH_ROOT to the directory the server may read files from")
	}
	if filePath == "" {
		return nil, 0, errors.New("file_path is required")
	}

	r, err := os.OpenRoot(root)
	if err != nil {
		fh.logger.Warn("file_path root not accessible", zap.String("root", root), zap.Error(err))
		return nil, 0, errors.New("file_path is not accessible (check SLACK_MCP_FILES_PATH_ROOT)")
	}
	defer r.Close()

	// os.Root names are relative to the root. Rewrite an absolute file_path that
	// points under the root into a root-relative name; one that points elsewhere
	// becomes a "../" path that the subsequent os.Root calls reject.
	name := filePath
	if filepath.IsAbs(name) {
		rel, err := filepath.Rel(root, name)
		if err != nil {
			fh.logger.Warn("file_path not relativizable to root", zap.String("file_path", filePath), zap.Error(err))
			return nil, 0, errors.New("file_path resolves outside the allowed directory")
		}
		name = rel
	}

	// Reject non-regular targets before opening (stat never blocks; opening a
	// FIFO would). Root.Stat applies the same escape check as Root.Open.
	info, err := r.Stat(name)
	if err != nil {
		fh.logger.Warn("file_path stat failed", zap.String("file_path", filePath), zap.Error(err))
		return nil, 0, errors.New("file_path not found or outside the allowed directory")
	}
	if !info.Mode().IsRegular() {
		return nil, 0, errors.New("file_path is not a regular file")
	}

	f, err := r.Open(name)
	if err != nil {
		fh.logger.Warn("file_path open failed", zap.String("file_path", filePath), zap.Error(err))
		return nil, 0, errors.New("file_path not found or outside the allowed directory")
	}

	// Re-stat the open handle for the authoritative size and a final regular-file
	// check on exactly what will be uploaded.
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		fh.logger.Warn("file_path fstat failed", zap.String("file_path", filePath), zap.Error(err))
		return nil, 0, errors.New("file_path is not accessible")
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, 0, errors.New("file_path is not a regular file")
	}
	if fi.Size() > maxFileSize {
		f.Close()
		return nil, 0, fmt.Errorf("file too large (%d bytes, max %d bytes)", fi.Size(), maxFileSize)
	}
	return f, int(fi.Size()), nil
}
