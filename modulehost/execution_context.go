package modulehost

import "context"

type executorContextKey struct{}

// WithExecutor binds the host transaction executor to an operation context.
// Persistence adapters resolve it before falling back to the host database.
func WithExecutor(ctx context.Context, executor DBTX) context.Context {
	if executor == nil {
		return ctx
	}
	return context.WithValue(ctx, executorContextKey{}, executor)
}

func ExecutorFromContext(ctx context.Context, fallback DBTX) DBTX {
	if ctx != nil {
		if executor, ok := ctx.Value(executorContextKey{}).(DBTX); ok && executor != nil {
			return executor
		}
	}
	return fallback
}
