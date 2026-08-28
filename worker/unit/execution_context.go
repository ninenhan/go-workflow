package unit

import "context"

type dispatchIDContextKey struct{}

// DispatchID returns the stable identity of the current node execution
// attempt. It is empty only when the caller supplied a legacy task without a
// dispatch_id. Units that perform external side effects should pass this value
// to the downstream system as its idempotency key.
func DispatchID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	dispatchID, _ := ctx.Value(dispatchIDContextKey{}).(string)
	return dispatchID
}

func withDispatchID(ctx context.Context, dispatchID string) context.Context {
	if dispatchID == "" {
		return ctx
	}
	return context.WithValue(ctx, dispatchIDContextKey{}, dispatchID)
}
