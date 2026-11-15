package dto

type ProviderResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Status  string `json:"status"`
	BaseURL string `json:"base_url,omitempty"`
}

type ProviderListResponse struct {
	Data []ProviderResponse `json:"data"`
}

type ProviderTestResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	LatencyMS int64  `json:"latency_ms"`
}

type APIErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
