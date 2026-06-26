package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

type huddleRoom struct {
	DateStart          int64    `json:"date_start"`
	DateEnd            int64    `json:"date_end"`
	ParticipantHistory []string `json:"participant_history"`
	CreatedBy          string   `json:"created_by"`
	HasEnded           bool     `json:"has_ended"`
}

type rawMessage struct {
	Ts      string      `json:"ts"`
	SubType string      `json:"subtype"`
	Room    *huddleRoom `json:"room,omitempty"`
}

type rawHistoryResponse struct {
	OK       bool         `json:"ok"`
	Messages []rawMessage `json:"messages"`
	Error    string       `json:"error,omitempty"`
}

// fetchHuddleRooms retrieves the raw room metadata for huddle messages.
// The slack-go library drops the "room" JSON field during deserialization,
// so we make direct HTTP calls to conversations.history for each timestamp.
func fetchHuddleRooms(httpClient *http.Client, token string, channelID string, timestamps []string, logger *zap.Logger) map[string]*huddleRoom {
	rooms := make(map[string]*huddleRoom, len(timestamps))

	for _, ts := range timestamps {
		room, err := fetchSingleHuddleRoom(httpClient, token, channelID, ts)
		if err != nil {
			logger.Warn("Failed to fetch huddle room metadata",
				zap.String("channel", channelID),
				zap.String("ts", ts),
				zap.Error(err),
			)
			continue
		}
		if room != nil {
			rooms[ts] = room
		}
	}

	return rooms
}

func fetchSingleHuddleRoom(httpClient *http.Client, token string, channelID string, ts string) (*huddleRoom, error) {
	form := url.Values{}
	form.Set("channel", channelID)
	form.Set("oldest", ts)
	form.Set("latest", ts)
	form.Set("limit", "1")
	form.Set("inclusive", "true")

	req, err := http.NewRequest(http.MethodPost, "https://slack.com/api/conversations.history", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	var histResp rawHistoryResponse
	if err := json.Unmarshal(body, &histResp); err != nil {
		return nil, fmt.Errorf("unmarshaling response: %w", err)
	}

	if !histResp.OK {
		return nil, fmt.Errorf("slack API error: %s", histResp.Error)
	}

	for _, msg := range histResp.Messages {
		if msg.Ts == ts && msg.Room != nil {
			return msg.Room, nil
		}
	}

	return nil, nil
}

// formatHuddleText produces a human-readable summary of a huddle.
func formatHuddleText(room *huddleRoom, usersMap map[string]slack.User) string {
	if room == nil || !room.HasEnded {
		return "Huddle in progress"
	}

	duration := formatDuration(room.DateEnd - room.DateStart)

	participants := make([]string, 0, len(room.ParticipantHistory))
	for _, uid := range room.ParticipantHistory {
		participants = append(participants, resolveUserName(uid, usersMap))
	}

	startedBy := resolveUserName(room.CreatedBy, usersMap)

	return fmt.Sprintf("Huddle (%s) - Participants: %s - Started by: %s",
		duration,
		strings.Join(participants, ", "),
		startedBy,
	)
}

func formatDuration(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}

	minutes := seconds / 60
	remainSec := seconds % 60

	if minutes < 60 {
		if remainSec > 0 {
			return fmt.Sprintf("%dm %ds", minutes, remainSec)
		}
		return fmt.Sprintf("%dm", minutes)
	}

	hours := minutes / 60
	remainMin := minutes % 60
	if remainMin > 0 {
		return fmt.Sprintf("%dh %dm", hours, remainMin)
	}
	return fmt.Sprintf("%dh", hours)
}

func resolveUserName(userID string, usersMap map[string]slack.User) string {
	if u, ok := usersMap[userID]; ok {
		return u.RealName
	}
	return userID
}
