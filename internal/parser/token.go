package parser

import (
	"fmt"

	"github.com/pkoukk/tiktoken-go"
)

// tokenCounter wraps tiktoken encoding for token counting.
type tokenCounter struct {
	enc *tiktoken.Tiktoken
}

// newTokenCounter creates a token counter using the given encoding name.
func newTokenCounter(encoding string) (*tokenCounter, error) {
	enc, err := tiktoken.GetEncoding(encoding)
	if err != nil {
		return nil, fmt.Errorf("get tiktoken encoding %s: %w", encoding, err)
	}
	return &tokenCounter{enc: enc}, nil
}

// Count returns the token count for the given text.
func (tc *tokenCounter) Count(text string) int {
	tokens := tc.enc.Encode(text, nil, nil)
	return len(tokens)
}
