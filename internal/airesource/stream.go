package airesource

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type StreamEvent struct {
	Type     string
	Delta    string
	Usage    UsageTokens
	Response *GenerateResponse
	Err      error
}

type streamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *UsageTokens `json:"usage"`
}

// Stream opens an OpenAI-compatible SSE stream through the same credential
// selection, concurrency, health and usage pipeline used by Generate.
func (r *Router) Stream(ctx context.Context, req GenerateRequest) (<-chan StreamEvent, error) {
	if req.Model == "" || len(req.Messages) == 0 {
		return nil, fmt.Errorf("model and messages are required")
	}
	if len(req.Tools) > 0 {
		return nil, ErrNativeToolCallingUnsupported
	}
	candidates, policy, err := r.service.Candidates(ctx, req.Model)
	if err != nil {
		return nil, err
	}
	compatible := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Provider.Protocol == ProtocolOpenAI {
			compatible = append(compatible, candidate)
		}
	}
	if len(compatible) == 0 {
		return nil, ErrStreamingUnsupported
	}
	maxAttempts := policy.MaxRetries + 1
	if maxAttempts > len(compatible) {
		maxAttempts = len(compatible)
	}
	excluded := map[string]bool{}
	var body io.ReadCloser
	var selected Candidate
	var last error
	var status int
	started := time.Now()
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate, ok := r.choose(req.Model, compatible, policy, excluded)
		if !ok {
			break
		}
		excluded[candidate.Credential.ID] = true
		body, status, last = openStream(ctx, candidate, req)
		if last == nil {
			selected = candidate
			break
		}
		r.release(candidate.Credential.ID)
		recordCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		recordErr := r.service.RecordResult(recordCtx, candidate, newID("req"), status, time.Since(started), last, UsageTokens{}, policy)
		cancel()
		if recordErr != nil {
			return nil, fmt.Errorf("record failed streaming request: %w", recordErr)
		}
		if !retryable(last) {
			return nil, last
		}
	}
	if body == nil {
		if last != nil {
			return nil, fmt.Errorf("all eligible streaming credentials failed: %w", last)
		}
		return nil, ErrNoCandidate
	}

	out := make(chan StreamEvent, 8)
	go func() {
		defer close(out)
		defer body.Close()
		defer r.release(selected.Credential.ID)

		var accumulated strings.Builder
		usage := UsageTokens{}
		responseID := newID("chatcmpl")
		modelName := req.Model
		finishReason := "stop"
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 64*1024), 8<<20)
		var streamErr error
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				break
			}
			if data == "" {
				continue
			}
			var chunk streamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				streamErr = fmt.Errorf("decode upstream stream event: %w", err)
				break
			}
			if chunk.ID != "" {
				responseID = chunk.ID
			}
			if chunk.Model != "" {
				modelName = chunk.Model
			}
			if chunk.Usage != nil {
				usage = *chunk.Usage
				select {
				case out <- StreamEvent{Type: "usage", Usage: usage}:
				case <-ctx.Done():
					streamErr = ctx.Err()
				}
			}
			if len(chunk.Choices) > 0 {
				choice := chunk.Choices[0]
				if choice.Delta.Content != "" {
					accumulated.WriteString(choice.Delta.Content)
					select {
					case out <- StreamEvent{Type: "text_delta", Delta: choice.Delta.Content}:
					case <-ctx.Done():
						streamErr = ctx.Err()
					}
				}
				if choice.FinishReason != nil && *choice.FinishReason != "" {
					finishReason = *choice.FinishReason
				}
			}
			if streamErr != nil {
				break
			}
		}
		if streamErr == nil {
			streamErr = scanner.Err()
		}
		latency := time.Since(started)
		result := GenerateResponse{
			ID: responseID, Object: "chat.completion", Created: time.Now().Unix(), Model: modelName,
			Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: accumulated.String()}, FinishReason: finishReason}},
			Usage:   usage,
		}
		recordCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		recordErr := r.service.RecordResult(recordCtx, selected, newID("req"), 200, latency, streamErr, usage, policy)
		cancel()
		if streamErr != nil {
			select {
			case out <- StreamEvent{Type: "error", Err: streamErr}:
			case <-ctx.Done():
			}
			return
		}
		if recordErr != nil {
			select {
			case out <- StreamEvent{Type: "error", Err: fmt.Errorf("record streaming usage: %w", recordErr)}:
			case <-ctx.Done():
			}
			return
		}
		select {
		case out <- StreamEvent{Type: "completed", Response: &result}:
		case <-ctx.Done():
		}
	}()
	return out, nil
}

func openStream(ctx context.Context, candidate Candidate, req GenerateRequest) (io.ReadCloser, int, error) {
	messages, _ := normalizeMessages(req.Messages)
	payload := map[string]any{
		"model":          candidate.UpstreamModel,
		"messages":       messages,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		payload["top_p"] = *req.TopP
	}
	if req.MaxTokens > 0 {
		payload["max_tokens"] = req.MaxTokens
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	target := endpointURL(candidate.Endpoint.BaseURL, "chat/completions")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+candidate.Secret)
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		return nil, 0, &providerRequestError{category: classifyTransportError(err)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8<<20))
		response.Body.Close()
		return nil, response.StatusCode, &upstreamError{Status: response.StatusCode}
	}
	return response.Body, response.StatusCode, nil
}
