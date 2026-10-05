package providers

import (
	"context"
	"errors"

	"github.com/kasuganosora/community/captcha/local"
	"github.com/kasuganosora/community/captcha/types"
)

// LocalProvider checks a one-time token issued by the built-in challenge.
type LocalProvider struct{}

// NewLocalProvider returns the built-in captcha provider.
func NewLocalProvider() *LocalProvider { return &LocalProvider{} }

// Name returns the provider id.
func (p *LocalProvider) Name() string { return "local" }

// GetSiteKey tells the web client to render the built-in widget.
func (p *LocalProvider) GetSiteKey() string { return "local" }

// IsConfigured is always true; this provider needs no third-party keys.
func (p *LocalProvider) IsConfigured() bool { return true }

// Verify burns the token. A copied or replayed token fails.
func (p *LocalProvider) Verify(ctx context.Context, response string, _ string) (*types.VerificationResult, error) {
	ok, err := local.ConsumeToken(ctx, response)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &types.VerificationResult{
			Success:    false,
			Provider:   "local",
			ErrorCodes: []string{"invalid-input-response"},
		}, errors.New("captcha verify failed")
	}
	return &types.VerificationResult{Success: true, Provider: "local"}, nil
}
