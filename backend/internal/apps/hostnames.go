package apps

import "context"

type hostnamesKey struct{}

// WithHostnames carries proposed authorities, never authority to publish them.
// The runtime validates their provider suffix and binds them to confirmation.
func WithHostnames(ctx context.Context, names map[string]string) context.Context {
	return context.WithValue(ctx, hostnamesKey{}, names)
}
func Hostname(ctx context.Context, pkg string) string {
	names, _ := ctx.Value(hostnamesKey{}).(map[string]string)
	return names[pkg]
}
