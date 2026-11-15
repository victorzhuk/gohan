package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

const (
	MetaKeyGohan  = "gohan"
	MetaKeyToken  = "token"
	MetaKeyTask   = "task"
	MetaKeyWakeAt = "wakeAt"

	MethodTasksGet = "tasks/get"
)

type TaskRef struct {
	TaskID string `json:"taskId"`
	RunID  string `json:"runId"`
}

type RequestStatePayload struct {
	Token string `json:"token"`
	Tool  string `json:"tool"`
}

func encodeRequestState(p RequestStatePayload) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("mcp: encode request state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeRequestState(s string) (RequestStatePayload, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return RequestStatePayload{}, fmt.Errorf("mcp: decode request state: %w", err)
	}
	var p RequestStatePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return RequestStatePayload{}, fmt.Errorf("mcp: decode request state: %w", err)
	}
	if p.Token == "" {
		return RequestStatePayload{}, fmt.Errorf("mcp: request state carries no resume token")
	}
	return p, nil
}
