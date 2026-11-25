package types

import "context"

type callerKey struct{}

// Caller identifies the authenticated principal making a request.
type Caller struct {
	AccessKeyID string
	// Name is a human-readable principal name (credential label).
	Name string
}

// WithCaller attaches the authenticated caller to ctx.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom returns the caller attached to ctx, if any.
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}
