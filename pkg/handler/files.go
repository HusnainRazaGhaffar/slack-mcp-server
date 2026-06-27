package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
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

// FilesSendHandler uploads a file to a Slack channel or thread.
func (fh *FilesHandler) FilesSendHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	channelID := request.GetString("channel_id", "")
	if channelID == "" {
		return nil, errors.New("channel_id is required")
	}
	if !isChannelAllowedForFiles(channelID) {
		return nil, fmt.Errorf("channel %s is not allowed for files_send; uploads are limited to the SLACK_MCP_ADD_MESSAGE_TOOL channels (set that variable to permit it)", channelID)
	}

	filename := request.GetString("filename", "")
	if filename == "" {
		return nil, errors.New("filename is required")
	}

	content := request.GetString("content", "")
	contentBase64 := request.GetString("content_base64", "")
	threadTs := request.GetString("thread_ts", "")
	initialComment := request.GetString("initial_comment", "")

	if content == "" && contentBase64 == "" {
		return nil, errors.New("either content or content_base64 is required")
	}
	if content != "" && contentBase64 != "" {
		return nil, errors.New("content and content_base64 are mutually exclusive")
	}

	var data []byte
	if contentBase64 != "" {
		var err error
		data, err = base64.StdEncoding.DecodeString(contentBase64)
		if err != nil {
			return nil, fmt.Errorf("failed to decode content_base64: %w", err)
		}
	} else {
		data = []byte(content)
	}

	if len(data) > maxFileSize {
		return nil, fmt.Errorf("file too large (%d bytes, max %d bytes)", len(data), maxFileSize)
	}

	params := slack.UploadFileParameters{
		Filename:        filename,
		FileSize:        len(data),
		Reader:          bytes.NewReader(data),
		Channel:         channelID,
		ThreadTimestamp: threadTs,
		InitialComment:  initialComment,
	}

	summary, err := fh.apiProvider.Slack().UploadFileContext(ctx, params)
	if err != nil {
		fh.logger.Error("UploadFileContext failed", zap.Error(err))
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}

	result := fmt.Sprintf("File uploaded successfully.\nFile ID: %s\nTitle: %s", summary.ID, summary.Title)
	return mcp.NewToolResultText(result), nil
}
