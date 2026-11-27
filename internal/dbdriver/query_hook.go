package dbdriver

import "context"

// membungkus satu SQL yang dijalankan driver di dalam operasi yang tidak menerima SQL dari luar . 
// Decorator observasi masang lewat context biar SQL yang benar-benar dikirim ke db tercatat di log dan trace, run mengembalikan jlh baris dibaca.
type QueryHook func(ctx context.Context, sql string, args []any, run func(ctx context.Context) (rows int64, err error)) error

type queryHookKey struct{}

// memasang hook untuk semua SQL yang dijalankan driver dengan ctx ini.
func WithQueryHook(ctx context.Context, hook QueryHook) context.Context {
	return context.WithValue(ctx, queryHookKey{}, hook)
}

func RunQuery(ctx context.Context, sql string, args []any, run func(ctx context.Context) (int64, error)) error {
	if hook, ok := ctx.Value(queryHookKey{}).(QueryHook); ok {
		return hook(ctx, sql, args, run)
	}
	_, err := run(ctx)
	return err
}
