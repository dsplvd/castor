package process

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stupside/castor/internal/telemetry"
	"github.com/urfave/cli/v3"
)

func TestCommandOwnsOneExporterAndReleasesIt(t *testing.T) {
	t.Setenv("CASTOR_TELEMETRY__ENDPOINT", "http://127.0.0.1:1")
	// Two sequential commands must each acquire and release the process exporter.
	// The nested action stands for CLI commands that compose embedded services.
	for range 2 {
		ran := false
		root := &cli.Command{Name: "castor-test", Commands: []*cli.Command{{Name: "cast", Action: func(ctx context.Context, _ *cli.Command) error {
			ran = true
			if !telemetry.Handler().Enabled(ctx, slog.LevelInfo) {
				t.Error("command has no telemetry")
			}
			return nil
		}}}}
		if err := run(t.Context(), root, []string{"castor-test", "cast"}, nil); err != nil {
			t.Fatal(err)
		}
		if !ran {
			t.Fatal("command did not run")
		}
		if telemetry.Handler().Enabled(t.Context(), slog.LevelInfo) {
			t.Fatal("command leaked exporter")
		}
	}
}

func TestBuildInfoDoesNotRequireWorkingRuntimeConfiguration(t *testing.T) {
	t.Setenv("CASTOR_TELEMETRY__ENDPOINT", "not a URL")
	if err := run(t.Context(), &cli.Command{Name: "castor-test"}, []string{"castor-test", "info"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestLocalOnlyActionDoesNotLoadRuntimeConfig(t *testing.T) {
	t.Setenv("CASTOR_TELEMETRY__ENDPOINT", "not a URL")
	ran := false
	root := &cli.Command{Name: "test", Action: func(context.Context, *cli.Command) error { ran = true; return nil }}
	if err := run(t.Context(), root, []string{"test", "--config", "missing-config.yaml"}, func(*cli.Command) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("local action did not run")
	}
}

func TestSignalDoesNotHideUnrelatedCommandFailure(t *testing.T) {
	failure := errors.New("writing final state failed")
	for _, tc := range []struct {
		name   string
		result error
		want   error
	}{
		{"unrelated failure", failure, failure},
		{"cleanup failure beside cancellation", errors.Join(context.Canceled, failure), failure},
		{"normal cancellation", context.Canceled, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			root := &cli.Command{Name: "test", Action: func(context.Context, *cli.Command) error { cancel(); return tc.result }}
			if err := run(ctx, root, []string{"test"}, func(*cli.Command) bool { return false }); !errors.Is(err, tc.want) {
				t.Fatalf("run=%v want %v", err, tc.want)
			}
		})
	}
}

func TestCancellationCauseFailureIsNotASuccessfulSignalStop(t *testing.T) {
	failure := errors.New("worker crashed")
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	root := &cli.Command{Name: "test", Action: func(context.Context, *cli.Command) error { cancel(failure); return failure }}
	if err := run(ctx, root, []string{"test"}, func(*cli.Command) bool { return false }); !errors.Is(err, failure) {
		t.Fatalf("run hid failure cause: %v", err)
	}
}
