package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

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

// FilesGetContentHandler retrieves file content from Slack by file ID.
func (fh *FilesHandler) FilesGetContentHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	fileID := request.GetString("file_id", "")
	if fileID == "" {
		return nil, errors.New("file_id is required")
	}

	file, _, _, err := fh.apiProvider.Slack().GetFileInfoContext(ctx, fileID, 0, 0)
	if err != nil {
		fh.logger.Error("GetFileInfoContext failed", zap.String("file_id", fileID), zap.Error(err))
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	if file.Size > maxFileSize {
		return nil, fmt.Errorf("file too large (%d bytes, max %d bytes)", file.Size, maxFileSize)
	}

	downloadURL := file.URLPrivateDownload
	if downloadURL == "" {
		downloadURL = file.URLPrivate
	}
	if downloadURL == "" {
		metadata := fmt.Sprintf("File: %s\nType: %s\nSize: %d bytes\nPermalink: %s\n(No download URL available)", file.Name, file.Mimetype, file.Size, file.Permalink)
		return mcp.NewToolResultText(metadata), nil
	}

	token := fh.apiProvider.Token()
	httpClient := fh.apiProvider.HTTPClient()

	if isTextFile(file.Mimetype, file.Name) {
		data, err := downloadFile(httpClient, downloadURL, token)
		if err != nil {
			return nil, fmt.Errorf("failed to download file: %w", err)
		}
		return mcp.NewToolResultText(string(data)), nil
	}

	if strings.HasPrefix(file.Mimetype, "image/") {
		data, err := downloadFile(httpClient, downloadURL, token)
		if err != nil {
			return nil, fmt.Errorf("failed to download image: %w", err)
		}
		encoded := base64.StdEncoding.EncodeToString(data)
		return &mcp.CallToolResult{
			Content: []mcp.Content{mcp.ImageContent{
				Type:     "image",
				Data:     encoded,
				MIMEType: file.Mimetype,
			}},
		}, nil
	}

	metadata := fmt.Sprintf("File: %s\nType: %s\nSize: %d bytes\nPermalink: %s\n(Binary file — content not downloaded)", file.Name, file.Mimetype, file.Size, file.Permalink)
	return mcp.NewToolResultText(metadata), nil
}

// FilesSendHandler uploads a file to a Slack channel or thread.
func (fh *FilesHandler) FilesSendHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	channelID := request.GetString("channel_id", "")
	if channelID == "" {
		return nil, errors.New("channel_id is required")
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

	params := slack.UploadFileV2Parameters{
		Filename:       filename,
		FileSize:       len(data),
		Reader:         bytes.NewReader(data),
		Channel:        channelID,
		ThreadTimestamp: threadTs,
		InitialComment: initialComment,
	}

	summary, err := fh.apiProvider.Slack().UploadFileV2Context(ctx, params)
	if err != nil {
		fh.logger.Error("UploadFileV2Context failed", zap.Error(err))
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}

	result := fmt.Sprintf("File uploaded successfully.\nFile ID: %s\nTitle: %s", summary.ID, summary.Title)
	return mcp.NewToolResultText(result), nil
}

func isTextFile(mimetype, filename string) bool {
	if strings.HasPrefix(mimetype, "text/") {
		return true
	}
	textMimeTypes := []string{
		"application/json",
		"application/xml",
		"application/javascript",
		"application/x-yaml",
		"application/toml",
	}
	for _, t := range textMimeTypes {
		if mimetype == t {
			return true
		}
	}
	textExtensions := []string{
		".md", ".yaml", ".yml", ".toml", ".csv", ".log",
		".json", ".xml", ".js", ".ts", ".py", ".go", ".rs",
		".sh", ".bash", ".txt",
	}
	lower := strings.ToLower(filename)
	for _, ext := range textExtensions {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func downloadFile(client *http.Client, url, token string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
