package llmsvc

import (
	"context"
	"errors"
	"sync"

	"github.com/kasuganosora/thinkbot/llm"
)

var (
	clientsMu sync.Mutex
	clients   = map[string]llm.Provider{} // provider id -> stats-recording client
)

func invalidateClients() {
	clientsMu.Lock()
	clients = map[string]llm.Provider{}
	clientsMu.Unlock()
}

// ErrNotConfigured is returned when a service has no usable provider.
var ErrNotConfigured = errors.New("llm provider not configured")

func clientFor(p ProviderConfig) (llm.Provider, error) {
	clientsMu.Lock()
	defer clientsMu.Unlock()
	if c, ok := clients[p.ID]; ok {
		return c, nil
	}
	inner, err := buildProvider(p)
	if err != nil {
		return nil, err
	}
	c := llm.NewStatsRecordingProvider(inner, &dbRecorder{provider: p}, p.ID)
	clients[p.ID] = c
	return c, nil
}

// testOverride lets tests replace the provider construction.
var testOverride func(ProviderConfig) (llm.Provider, error)

// SetTestProviderFactory installs a factory used instead of real providers
// (tests only). Passing nil restores the default.
func SetTestProviderFactory(f func(ProviderConfig) (llm.Provider, error)) {
	clientsMu.Lock()
	testOverride = f
	clients = map[string]llm.Provider{}
	clientsMu.Unlock()
}

// Generate runs a single non-streaming request for feature through p,
// filling model and default parameters from the provider config. Token usage
// and cost are recorded per day/feature/provider/model.
func Generate(ctx context.Context, p ProviderConfig, feature string, params llm.GenerateParams) (*llm.GenerateResult, error) {
	var (
		client llm.Provider
		err    error
	)
	clientsMu.Lock()
	override := testOverride
	clientsMu.Unlock()
	if override != nil {
		inner, ferr := override(p)
		if ferr != nil {
			return nil, ferr
		}
		client = llm.NewStatsRecordingProvider(inner, &dbRecorder{provider: p}, p.ID)
	} else if client, err = clientFor(p); err != nil {
		return nil, err
	}
	if params.Model == nil {
		params.Model = llm.ChatModel(p.Model)
	}
	if params.MaxTokens == nil && p.MaxTokens > 0 {
		v := p.MaxTokens
		params.MaxTokens = &v
	}
	if params.Temperature == nil && p.Temperature != nil {
		v := *p.Temperature
		params.Temperature = &v
	}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout()+5e9)
	defer cancel()
	ctx = llm.WithStatsFeature(ctx, feature)
	result, err := client.DoGenerate(ctx, params)
	if err != nil {
		recordFailure(p, feature, params.Model.ID)
		return nil, err
	}
	if result.Usage.TotalTokens <= 0 {
		// StatsRecordingProvider skips zero-token results; still count the call.
		addUsage(UsageDaily{Day: Today(), Feature: feature, ProviderID: p.ID, Model: params.Model.ID, Calls: 1, Currency: p.Currency})
	}
	return result, nil
}

// TestResult is the outcome of a connection test.
type TestResult struct {
	OK        bool   `json:"ok"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
	Model     string `json:"model"`
	Reply     string `json:"reply,omitempty"`
}

// ErrorCode maps an llm error to a stable code. The message of the provider is
// never returned to clients because it may echo request data.
func ErrorCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "llm_timeout"
	}
	if le, ok := llm.AsLLMError(err); ok {
		switch le.Reason {
		case llm.ErrorReasonAuthentication:
			return "llm_auth_failed"
		case llm.ErrorReasonRateLimit, llm.ErrorReasonQuotaExceeded:
			return "llm_rate_limited"
		case llm.ErrorReasonInvalidRequest:
			return "llm_invalid_request"
		case llm.ErrorReasonContentPolicy:
			return "llm_content_policy"
		case llm.ErrorReasonTransport:
			return "llm_unreachable"
		}
	}
	return "llm_request_failed"
}
