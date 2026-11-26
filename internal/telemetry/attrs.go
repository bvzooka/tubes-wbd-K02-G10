package telemetry

import "context"

type logAttrsKey struct{}

// WithLogAttrs menempelkan pasangan nama dan nilai ke context, semua log yang memakai context itu ikut mencatatnya
// dipakai supaya log SQL di database target tahu SQL itu milik eksekusi yang mana
func WithLogAttrs(ctx context.Context, args ...any) context.Context {
	// disalin supaya context turunan tidak mengubah milik induknya
	merged := append(append([]any{}, logAttrs(ctx)...), args...)
	return context.WithValue(ctx, logAttrsKey{}, merged)
}

func logAttrs(ctx context.Context) []any {
	args, _ := ctx.Value(logAttrsKey{}).([]any)
	return args
}
