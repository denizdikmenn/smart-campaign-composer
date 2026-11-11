// Package llm holds what every Claude-backed feature shares: client
// construction, the default model and the mapping of API failures to a small
// set of typed errors the HTTP layer can turn into useful messages.
package llm

import (
	"errors"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// DefaultModel is used when ANTHROPIC_MODEL is not set.
const DefaultModel = "claude-haiku-5-5"

// Typed errors so the API layer can show a useful message.
var (
	ErrAuth     = errors.New("the Anthropic API key was rejected")
	ErrRetry    = errors.New("the AI service is busy or unavailable, please retry")
	ErrRefusal  = errors.New("the AI declined to process this brief")
	ErrBadReply = errors.New("the AI returned an unusable response")
)

// NewClient builds an Anthropic client with one SDK retry. Extra options
// (e.g. a test base URL) are appended.
func NewClient(apiKey string, opts ...option.RequestOption) anthropic.Client {
	all := append([]option.RequestOption{option.WithAPIKey(apiKey), option.WithMaxRetries(1)}, opts...)
	return anthropic.NewClient(all...)
}

// Model returns model, or DefaultModel when it is empty.
func Model(model string) string {
	if model == "" {
		return DefaultModel
	}
	return model
}

// ClassifyError maps an SDK/transport error to one of the typed errors.
// Status codes other than auth/overload are returned as plain errors.
func ClassifyError(err error) error {
	var apierr *anthropic.Error
	if errors.As(err, &apierr) {
		switch {
		case apierr.StatusCode == 401 || apierr.StatusCode == 403:
			return ErrAuth
		case apierr.StatusCode == 429 || apierr.StatusCode >= 500:
			return ErrRetry
		}
		return fmt.Errorf("anthropic request rejected (status %d)", apierr.StatusCode)
	}
	return fmt.Errorf("%w: %v", ErrRetry, err)
}

// Price is the list price in USD per million tokens. Rates change; this table
// is for cost transparency in the UI, not billing.
type Price struct{ Input, Output float64 }

var prices = map[string]Price{
	"claude-haiku-5-5":  {0.10, 0.50},
	"claude-sonnet-5-5": {2, 10},
	"claude-opus-5-5":   {4, 20},
}

// PriceOf returns the price of model, if known.
func PriceOf(model string) (Price, bool) {
	p, ok := prices[model]
	return p, ok
}

// USD is the cost of a token count at this price.
func (p Price) USD(input, output int) float64 {
	return (float64(input)*p.Input + float64(output)*p.Output) / 1e6
}
