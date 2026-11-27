package dbdriver

import (
	"context"
	"errors"
	"testing"
)

func TestRunQuery_WithoutHookRunsDirectly(t *testing.T) {
	ran := false
	err := RunQuery(context.Background(), "SELECT 1", nil, func(context.Context) (int64, error) {
		ran = true
		return 1, nil
	})
	if err != nil || !ran {
		t.Fatalf("ran = %v, err = %v", ran, err)
	}
}

func TestRunQuery_HookSeesSQLArgsAndResult(t *testing.T) {
	failure := errors.New("gagal")
	var gotSQL string
	var gotArgs []any
	var gotRows int64
	ctx := WithQueryHook(context.Background(), func(ctx context.Context, sql string, args []any, run func(context.Context) (int64, error)) error {
		gotSQL, gotArgs = sql, args
		var err error
		gotRows, err = run(ctx)
		return err
	})

	err := RunQuery(ctx, "SELECT * FROM t LIMIT $1", []any{51}, func(context.Context) (int64, error) {
		return 3, failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("error dari run harus diteruskan, dapat %v", err)
	}
	if gotSQL != "SELECT * FROM t LIMIT $1" || len(gotArgs) != 1 || gotArgs[0] != 51 || gotRows != 3 {
		t.Errorf("hook melihat sql=%q args=%v rows=%d", gotSQL, gotArgs, gotRows)
	}
}
