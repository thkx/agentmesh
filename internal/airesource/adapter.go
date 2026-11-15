package airesource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type upstreamError struct {
	Status  int
	Message string
}

func (e *upstreamError) Error() string {
	return fmt.Sprintf("upstream request failed with status %d", e.Status)
}
func endpointURL(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}
func doJSON(ctx context.Context, client *http.Client, method, target, key string, protocol Protocol, payload any) ([]byte, int, error) {
	var body io.Reader
	if payload != nil {
		b, e := json.Marshal(payload)
		if e != nil {
			return nil, 0, e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, target, body)
	if e != nil {
		return nil, 0, e
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	switch protocol {
	case ProtocolAnthropic:
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	case ProtocolGemini:
		u, _ := url.Parse(target)
		q := u.Query()
		q.Set("key", key)
		u.RawQuery = q.Encode()
		req.URL = u
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, e := client.Do(req)
	if e != nil {
		// net/http errors may include the full URL. Gemini authentication uses
		// a query parameter, so never return the raw error string.
		return nil, 0, errors.New("provider request failed (network or timeout)")
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if e != nil {
		return nil, resp.StatusCode, e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = data // Upstream bodies are deliberately not surfaced; they may echo secrets.
		return nil, resp.StatusCode, &upstreamError{Status: resp.StatusCode}
	}
	return data, resp.StatusCode, nil
}
func normalizeMessages(messages []Message) ([]map[string]string, string) {
	out := make([]map[string]string, 0, len(messages))
	system := []string{}
	for _, m := range messages {
		if m.Role == "system" || m.Role == "developer" {
			system = append(system, m.Content)
			continue
		}
		role := m.Role
		if role != "assistant" {
			role = "user"
		}
		out = append(out, map[string]string{"role": role, "content": m.Content})
	}
	return out, strings.Join(system, "\n\n")
}
func generate(ctx context.Context, c Candidate, req GenerateRequest) (GenerateResponse, int, error) {
	timeout := time.Duration(c.Endpoint.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	messages, system := normalizeMessages(req.Messages)
	var raw []byte
	var status int
	var err error
	switch c.Provider.Protocol {
	case ProtocolOpenAI:
		payload := map[string]any{"model": c.UpstreamModel, "messages": messages, "stream": false}
		if req.Temperature != nil {
			payload["temperature"] = *req.Temperature
		}
		if req.TopP != nil {
			payload["top_p"] = *req.TopP
		}
		if req.MaxTokens > 0 {
			payload["max_tokens"] = req.MaxTokens
		}
		raw, status, err = doJSON(ctx, client, http.MethodPost, endpointURL(c.Endpoint.BaseURL, "chat/completions"), c.Secret, c.Provider.Protocol, payload)
	case ProtocolAnthropic:
		payload := map[string]any{"model": c.UpstreamModel, "messages": messages, "max_tokens": req.MaxTokens}
		if req.MaxTokens <= 0 {
			payload["max_tokens"] = 2048
		}
		if system != "" {
			payload["system"] = system
		}
		if req.Temperature != nil {
			payload["temperature"] = *req.Temperature
		}
		raw, status, err = doJSON(ctx, client, http.MethodPost, endpointURL(c.Endpoint.BaseURL, "v1/messages"), c.Secret, c.Provider.Protocol, payload)
	case ProtocolGemini:
		contents := make([]map[string]any, 0, len(messages))
		for _, m := range messages {
			contents = append(contents, map[string]any{"role": m["role"], "parts": []map[string]string{{"text": m["content"]}}})
		}
		payload := map[string]any{"contents": contents}
		if system != "" {
			payload["systemInstruction"] = map[string]any{"parts": []map[string]string{{"text": system}}}
		}
		gen := map[string]any{}
		if req.Temperature != nil {
			gen["temperature"] = *req.Temperature
		}
		if req.TopP != nil {
			gen["topP"] = *req.TopP
		}
		if req.MaxTokens > 0 {
			gen["maxOutputTokens"] = req.MaxTokens
		}
		if len(gen) > 0 {
			payload["generationConfig"] = gen
		}
		raw, status, err = doJSON(ctx, client, http.MethodPost, endpointURL(c.Endpoint.BaseURL, "v1beta/models/"+url.PathEscape(c.UpstreamModel)+":generateContent"), c.Secret, c.Provider.Protocol, payload)
	default:
		return GenerateResponse{}, 0, ErrUnsupportedProtocol
	}
	if err != nil {
		return GenerateResponse{}, status, err
	}
	switch c.Provider.Protocol {
	case ProtocolOpenAI:
		var out GenerateResponse
		if err = json.Unmarshal(raw, &out); err != nil {
			return out, status, fmt.Errorf("decode OpenAI-compatible response: %w", err)
		}
		out.Model = req.Model
		return out, status, nil
	case ProtocolAnthropic:
		var x struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			StopReason string `json:"stop_reason"`
			Usage      struct {
				Input  int `json:"input_tokens"`
				Output int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err = json.Unmarshal(raw, &x); err != nil {
			return GenerateResponse{}, status, err
		}
		text := strings.Builder{}
		for _, v := range x.Content {
			if v.Type == "text" {
				text.WriteString(v.Text)
			}
		}
		return GenerateResponse{ID: x.ID, Object: "chat.completion", Created: time.Now().Unix(), Model: req.Model, Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: text.String()}, FinishReason: x.StopReason}}, Usage: UsageTokens{PromptTokens: x.Usage.Input, CompletionTokens: x.Usage.Output, TotalTokens: x.Usage.Input + x.Usage.Output}}, status, nil
	case ProtocolGemini:
		var x struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			Usage struct {
				Prompt int `json:"promptTokenCount"`
				Output int `json:"candidatesTokenCount"`
				Total  int `json:"totalTokenCount"`
			} `json:"usageMetadata"`
		}
		if err = json.Unmarshal(raw, &x); err != nil {
			return GenerateResponse{}, status, err
		}
		var text strings.Builder
		reason := "stop"
		if len(x.Candidates) > 0 {
			for _, p := range x.Candidates[0].Content.Parts {
				text.WriteString(p.Text)
			}
			if x.Candidates[0].FinishReason != "STOP" && x.Candidates[0].FinishReason != "" {
				reason = x.Candidates[0].FinishReason
			}
		}
		return GenerateResponse{ID: newID("gen"), Object: "chat.completion", Created: time.Now().Unix(), Model: req.Model, Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: text.String()}, FinishReason: reason}}, Usage: UsageTokens{PromptTokens: x.Usage.Prompt, CompletionTokens: x.Usage.Output, TotalTokens: x.Usage.Total}}, status, nil
	}
	return GenerateResponse{}, status, errors.New("unreachable protocol")
}
func probeEndpoint(ctx context.Context, protocol Protocol, base, key string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	var target string
	switch protocol {
	case ProtocolOpenAI:
		target = endpointURL(base, "models")
	case ProtocolAnthropic:
		target = endpointURL(base, "v1/models")
	case ProtocolGemini:
		target = endpointURL(base, "v1beta/models")
	default:
		return ErrUnsupportedProtocol
	}
	_, status, err := doJSON(ctx, client, http.MethodGet, target, key, protocol, nil)
	if err == nil {
		return nil
	}
	var ue *upstreamError
	if errors.As(err, &ue) && ue.Status == 404 {
		return nil
	}
	if status == 405 {
		return nil
	}
	return err
}

func discoverModels(ctx context.Context, protocol Protocol, baseURL, key string, timeout time.Duration) ([]string, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	var target string
	switch protocol {
	case ProtocolOpenAI:
		target = endpointURL(baseURL, "models")
	case ProtocolAnthropic:
		target = endpointURL(baseURL, "v1/models")
	case ProtocolGemini:
		target = endpointURL(baseURL, "v1beta/models")
	default:
		return nil, ErrUnsupportedProtocol
	}
	data, _, err := doJSON(ctx, client, http.MethodGet, target, key, protocol, nil)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0)
	switch protocol {
	case ProtocolOpenAI:
		var v struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err = json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		for _, item := range v.Data {
			if item.ID != "" {
				models = append(models, item.ID)
			}
		}
	case ProtocolAnthropic:
		var v struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err = json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		for _, item := range v.Data {
			if item.ID != "" {
				models = append(models, item.ID)
			}
		}
	case ProtocolGemini:
		var v struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err = json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		for _, item := range v.Models {
			name := strings.TrimPrefix(item.Name, "models/")
			if name != "" {
				models = append(models, name)
			}
		}
	}
	return models, nil
}
