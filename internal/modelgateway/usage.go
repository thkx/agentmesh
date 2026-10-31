package modelgateway

type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`

	CachedInputTokens int64 `json:"cached_input_tokens,omitempty"`
	ReasoningTokens   int64 `json:"reasoning_tokens,omitempty"`
}

func (u *Usage) Normalize() {
	if u == nil {
		return
	}

	if u.InputTokens < 0 {
		u.InputTokens = 0
	}

	if u.OutputTokens < 0 {
		u.OutputTokens = 0
	}

	if u.CachedInputTokens < 0 {
		u.CachedInputTokens = 0
	}

	if u.ReasoningTokens < 0 {
		u.ReasoningTokens = 0
	}

	u.TotalTokens = u.InputTokens + u.OutputTokens
}
