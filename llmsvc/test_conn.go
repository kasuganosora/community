package llmsvc

import (
	"context"
	"strings"
	"time"

	"github.com/kasuganosora/thinkbot/llm"
)

// FeatureConnectionTest is the usage feature name of admin connection tests.
const FeatureConnectionTest = "connection_test"

// Test sends a tiny request through p and reports latency. Provider error
// texts are not returned (they may contain request data); only stable codes.
func Test(ctx context.Context, p ProviderConfig) TestResult {
	start := time.Now()
	maxTokens := 16
	res, err := Generate(ctx, p, FeatureConnectionTest, llm.GenerateParams{
		System:    "You are a connectivity check. Reply with the single word OK.",
		Messages:  []llm.Message{llm.UserMessage("ping")},
		MaxTokens: &maxTokens,
	})
	out := TestResult{LatencyMS: time.Since(start).Milliseconds(), Model: p.Model}
	if err != nil {
		out.Code = ErrorCode(err)
		return out
	}
	out.OK = true
	reply := strings.TrimSpace(res.Text)
	if r := []rune(reply); len(r) > 40 {
		reply = string(r[:40])
	}
	out.Reply = reply
	return out
}
