package types

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"sync/atomic"
	"time"
)

// Wire protocol constants.
const (
	// JSONContentType is the AWS JSON 1.1 content type used by SSM.
	JSONContentType = "application/x-amz-json-1.1"
	// TargetPrefix prefixes every X-Amz-Target header value.
	TargetPrefix = "AmazonSSM."
)

// Timestamp is a time.Time serialized as fractional epoch seconds, which is
// the AWS JSON 1.1 encoding for timestamp members.
type Timestamp struct {
	time.Time
}

// NewTimestamp wraps t. Returns nil for the zero time so optional
// timestamps are omitted from JSON.
func NewTimestamp(t time.Time) *Timestamp {
	if t.IsZero() {
		return nil
	}
	return &Timestamp{Time: t}
}

// MarshalJSON implements json.Marshaler.
func (t Timestamp) MarshalJSON() ([]byte, error) {
	secs := float64(t.UnixMilli()) / 1000
	return []byte(strconv.FormatFloat(secs, 'f', 3, 64)), nil
}

// UnmarshalJSON accepts epoch seconds (number) or RFC 3339 strings.
func (t *Timestamp) UnmarshalJSON(data []byte) error {
	var f float64
	if err := json.Unmarshal(data, &f); err == nil {
		sec, frac := math.Modf(f)
		t.Time = time.Unix(int64(sec), int64(math.Round(frac*1e3))*int64(time.Millisecond)).UTC()
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("invalid timestamp %s", string(data))
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("invalid timestamp %q: %w", s, err)
	}
	t.Time = parsed
	return nil
}

// Tag is an AWS resource tag.
type Tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// Target selects managed instances by tag or ID (SendCommand Targets).
type Target struct {
	Key    string   `json:"Key"`
	Values []string `json:"Values"`
}

// nowFunc is the injectable clock used by all domain packages.
var nowFunc atomic.Pointer[func() time.Time]

// Now returns the current UTC time from the injectable clock.
func Now() time.Time {
	if f := nowFunc.Load(); f != nil {
		return (*f)().UTC()
	}
	return time.Now().UTC()
}

// SetNowFunc overrides the clock (tests only). Pass nil to restore.
func SetNowFunc(f func() time.Time) {
	if f == nil {
		nowFunc.Store(nil)
		return
	}
	nowFunc.Store(&f)
}

// EncodeNextToken encodes an opaque pagination offset.
func EncodeNextToken(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

// DecodeNextToken decodes a token from EncodeNextToken; empty means 0.
func DecodeNextToken(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, NewInvalidNextToken()
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0, NewInvalidNextToken()
	}
	return n, nil
}

// Paginate slices items according to nextToken/maxResults and returns the
// page plus the token for the following page ("" when exhausted).
// limit is the largest allowed maxResults (0 = unlimited).
func Paginate[T any](items []T, nextToken string, maxResults, defaultMax, limit int) ([]T, string, error) {
	offset, err := DecodeNextToken(nextToken)
	if err != nil {
		return nil, "", err
	}
	if maxResults <= 0 {
		maxResults = defaultMax
	}
	if limit > 0 && maxResults > limit {
		return nil, "", NewValidationError("1 validation error detected: Value '%d' at 'maxResults' failed to satisfy constraint: Member must have value less than or equal to %d", maxResults, limit)
	}
	if offset > len(items) {
		return nil, "", NewInvalidNextToken()
	}
	end := min(offset+maxResults, len(items))
	next := ""
	if end < len(items) {
		next = EncodeNextToken(end)
	}
	return items[offset:end], next, nil
}
