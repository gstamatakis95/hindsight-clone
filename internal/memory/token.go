package memory

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
)

// pageSize applies the default and the cap to a requested page size.
func pageSize(n int) int {
	switch {
	case n <= 0:
		return defaultPageSize
	case n > maxPageSize:
		return maxPageSize
	default:
		return n
	}
}

// encodeToken returns the opaque page token of a keyset position.
func encodeToken(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode page token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// decodeToken reads a page token produced by encodeToken; a malformed one is domain.ErrInvalidArgument.
func decodeToken(tok string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return fmt.Errorf("%w: malformed page token: %w", domain.ErrInvalidArgument, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%w: malformed page token: %w", domain.ErrInvalidArgument, err)
	}
	return nil
}
