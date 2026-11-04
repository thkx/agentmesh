package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const apiPrefix = "/api/v1"

type ClientConfig struct {
	BaseURL string
	Timeout time.Duration
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type APIError struct {
	StatusCode int
	Code       string `json:"code"`
	Message    string `json:"message"`
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf(
			"API error (%d): %s: %s",
			e.StatusCode,
			e.Code,
			e.Message,
		)
	}

	return fmt.Sprintf("API error (%d): %s", e.StatusCode, e.Message)
}

func NewClient(cfg ClientConfig) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("server address is required")
	}

	parsed, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil {
		return nil, fmt.Errorf("parse server address: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("server address must use http or https")
	}

	if parsed.Host == "" {
		return nil, errors.New("server address must contain a host")
	}

	if cfg.Timeout <= 0 {
		return nil, errors.New("timeout must be greater than zero")
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/")

	return &Client{
		baseURL: parsed.String(),
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   10,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
			},
		},
	}, nil
}

func (c *Client) Get(
	ctx context.Context,
	path string,
	result any,
) error {
	return c.do(ctx, http.MethodGet, path, nil, result)
}

func (c *Client) Post(
	ctx context.Context,
	path string,
	body any,
	result any,
) error {
	return c.do(ctx, http.MethodPost, path, body, result)
}

func (c *Client) do(
	ctx context.Context,
	method string,
	path string,
	body any,
	result any,
) error {
	endpoint, err := c.buildURL(path)
	if err != nil {
		return err
	}

	var requestBody io.Reader

	if body != nil {
		data, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return fmt.Errorf("encode request: %w", marshalErr)
		}

		requestBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		method,
		endpoint,
		requestBody,
	)
	if err != nil {
		return fmt.Errorf("create HTTP request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("request timed out: %w", err)
		}

		return fmt.Errorf("request AgentMesh Runtime: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("read API response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeAPIError(resp.StatusCode, data)
	}

	if result == nil || len(data) == 0 {
		return nil
	}

	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("decode API response: %w", err)
	}

	return nil
}

func (c *Client) buildURL(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("API path is required")
	}

	if strings.HasPrefix(path, "http://") ||
		strings.HasPrefix(path, "https://") {
		return "", errors.New("absolute URLs are not allowed")
	}

	return c.baseURL + apiPrefix + "/" + strings.TrimLeft(path, "/"), nil
}

func decodeAPIError(statusCode int, data []byte) error {
	apiErr := &APIError{
		StatusCode: statusCode,
		Message:    http.StatusText(statusCode),
	}

	var response struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}

	if err := json.Unmarshal(data, &response); err == nil {
		apiErr.Code = response.Code

		switch {
		case response.Message != "":
			apiErr.Message = response.Message
		case response.Error != "":
			apiErr.Message = response.Error
		}
	} else if len(data) > 0 {
		apiErr.Message = strings.TrimSpace(string(data))
	}

	return apiErr
}

func escapePathSegment(value string) string {
	return url.PathEscape(value)
}
