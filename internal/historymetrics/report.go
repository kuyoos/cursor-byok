package historymetrics

type Attribution struct {
	ProviderID       string `json:"providerID"`
	ModelConfigID    string `json:"modelConfigID,omitempty"`
	Model            string `json:"model,omitempty"`
	ProviderCalls    int64  `json:"providerCalls"`
	InputTokens      int64  `json:"inputTokens"`
	OutputTokens     int64  `json:"outputTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CacheWriteTokens int64  `json:"cacheWriteTokens"`
	TotalTokens      int64  `json:"totalTokens"`
}

type Daily struct {
	Date              string `json:"date"`
	ProviderCalls     int64  `json:"providerCalls"`
	TurnsTotal        int64  `json:"turnsTotal"`
	ValidTurnsTotal   int64  `json:"validTurnsTotal"`
	InvalidTurnsTotal int64  `json:"invalidTurnsTotal"`
	InputTokens       int64  `json:"inputTokens"`
	OutputTokens      int64  `json:"outputTokens"`
	CacheReadTokens   int64  `json:"cacheReadTokens"`
	CacheWriteTokens  int64  `json:"cacheWriteTokens"`
	TotalTokens       int64  `json:"totalTokens"`
}

type UsageEvent struct {
	EventID          string `json:"eventID"`
	Kind             string `json:"kind,omitempty"`
	Status           string `json:"status,omitempty"`
	ProviderID       string `json:"providerID,omitempty"`
	ModelConfigID    string `json:"modelConfigID,omitempty"`
	Model            string `json:"model,omitempty"`
	At               string `json:"at"`
	InputTokens      int64  `json:"inputTokens"`
	OutputTokens     int64  `json:"outputTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CacheWriteTokens int64  `json:"cacheWriteTokens"`
	TotalTokens      int64  `json:"totalTokens"`
	UsagePresent     bool   `json:"usagePresent"`
}

type Summary struct {
	ProviderCallsTotal int                    `json:"providerCallsTotal"`
	TurnsTotal         int                    `json:"turnsTotal"`
	ValidTurnsTotal    int                    `json:"validTurnsTotal"`
	InvalidTurnsTotal  int                    `json:"invalidTurnsTotal"`
	RequestTokensTotal int64                  `json:"requestTokensTotal"`
	PromptTokensTotal  int64                  `json:"promptTokensTotal"`
	CacheReadTokens    int64                  `json:"cacheReadTokens"`
	CacheWriteTokens   int64                  `json:"cacheWriteTokens"`
	CacheHitRate       *float64               `json:"cacheHitRate"`
	Daily              []Daily                `json:"daily"`
	RecentEvents       []UsageEvent           `json:"recentEvents"`
	ByProvider         map[string]Attribution `json:"byProvider"`
	ByProviderModel    map[string]Attribution `json:"byProviderModel"`
}

type Totals struct {
	InputTokens        int64
	OutputTokens       int64
	CacheReadTokens    int64
	CacheWriteTokens   int64
	PromptTokensTotal  int64
	RequestTokensTotal int64
}

func cacheHitRateFromTotals(totals Totals) *float64 {
	inputCacheTokensTotal := totals.CacheReadTokens + totals.InputTokens
	if inputCacheTokensTotal <= 0 {
		return nil
	}
	value := float64(totals.CacheReadTokens) / float64(inputCacheTokensTotal)
	return &value
}
