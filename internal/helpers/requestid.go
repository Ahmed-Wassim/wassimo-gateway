package helpers

import "context"

type ctxKey string

const key ctxKey = "requestID"

func FromContext(ctx context.Context) string {
	if id, ok := ctx.Value(key).(string); ok {
		return id
	}
	return ""
}

func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, key, id)
}
