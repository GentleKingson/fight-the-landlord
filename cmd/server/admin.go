package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

const maxAdminResponseBytes = 64 << 10

type adminCommandPayload struct {
	PlayerID       string `json:"player_id,omitempty"`
	DurationSecond int64  `json:"duration_seconds,omitempty"`
}

func runAdminCommand(action, playerID string, duration time.Duration, output io.Writer) error {
	key := os.Getenv("ADMIN_KEY")
	if key == "" {
		return fmt.Errorf("ADMIN_KEY is required")
	}
	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = "1780"
	}
	if value, err := strconv.Atoi(port); err != nil || value < 1 || value > 65535 {
		return fmt.Errorf("SERVER_PORT must be between 1 and 65535")
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return executeAdminCommand(client, "http://127.0.0.1:"+port, key, action, playerID, duration, output)
}

func executeAdminCommand(
	client *http.Client,
	baseURL, key, action, playerID string,
	duration time.Duration,
	output io.Writer,
) error {
	method, payload, err := buildAdminCommand(action, playerID, duration)
	if err != nil {
		return err
	}
	var body io.ReadCloser = http.NoBody
	if payload != nil {
		encoded, encodeErr := json.Marshal(payload)
		if encodeErr != nil {
			return fmt.Errorf("encode admin request: %w", encodeErr)
		}
		body = io.NopCloser(bytes.NewReader(encoded))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, baseURL+"/admin/"+action, body)
	if err != nil {
		return fmt.Errorf("create admin request: %w", err)
	}
	request.Header.Set("X-Admin-Key", key)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("send admin request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxAdminResponseBytes))
	if err != nil {
		return fmt.Errorf("read admin response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("admin request failed with %s: %s", response.Status, bytes.TrimSpace(responseBody))
	}
	if len(responseBody) != 0 && output != nil {
		if _, err := output.Write(responseBody); err != nil {
			return fmt.Errorf("write admin response: %w", err)
		}
	}
	return nil
}

func buildAdminCommand(
	action, playerID string,
	duration time.Duration,
) (requestMethod string, requestPayload any, buildErr error) {
	switch action {
	case "status":
		return http.MethodGet, nil, nil
	case "drain", "maintenance", "resume":
		return http.MethodPost, nil, nil
	case "disconnect", "unmute", "unban":
		if playerID == "" {
			return "", nil, fmt.Errorf("-admin-player is required for %s", action)
		}
		return http.MethodPost, adminCommandPayload{PlayerID: playerID}, nil
	case "mute", "ban":
		if playerID == "" {
			return "", nil, fmt.Errorf("-admin-player is required for %s", action)
		}
		if duration < time.Second || duration%time.Second != 0 {
			return "", nil, fmt.Errorf("-admin-duration must be a positive whole number of seconds")
		}
		return http.MethodPost, adminCommandPayload{
			PlayerID:       playerID,
			DurationSecond: int64(duration / time.Second),
		}, nil
	default:
		return "", nil, fmt.Errorf("unsupported admin action %q", action)
	}
}
