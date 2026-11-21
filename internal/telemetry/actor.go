package telemetry

import "context"

type actorKey struct{}

// WithActorSlot menyiapkan tempat nama user di context. Middleware logging membuatnya di
// awal request, middleware auth mengisinya, lalu log http.request bisa membacanya.
func WithActorSlot(ctx context.Context) (context.Context, *string) {
	slot := new(string)
	return context.WithValue(ctx, actorKey{}, slot), slot
}

func SetActor(ctx context.Context, username string) {
	if slot, ok := ctx.Value(actorKey{}).(*string); ok {
		*slot = username
	}
}
