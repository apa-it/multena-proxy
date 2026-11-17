package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// withRestoredGlobalTracerProvider snapshots the current global
// OTel TracerProvider, runs fn, then restores it, so tests don't leak
// state into each other or the rest of the test binary.
func withRestoredGlobalTracerProvider(t *testing.T, fn func()) {
	t.Helper()
	prev := otel.GetTracerProvider()
	defer otel.SetTracerProvider(prev)
	fn()
}

// TestWithTracing_Disabled_LeavesGlobalProviderUntouched is the "flag off" case:
// when tracing.enabled (and OTEL_TRACING_ENABLED) is false, WithTracing must not
// initialize or install any SDK tracer provider. The global provider stays
// whatever no-op implementation it already was, so there is no overhead.
func TestWithTracing_Disabled_LeavesGlobalProviderUntouched(t *testing.T) {
	withRestoredGlobalTracerProvider(t, func() {
		sentinel := noop.NewTracerProvider()
		otel.SetTracerProvider(sentinel)

		a := &App{Cfg: &Config{Tracing: TracingConfig{Enabled: false}}}
		a.WithTracing()

		if got := otel.GetTracerProvider(); got != sentinel {
			t.Fatalf("expected global tracer provider to be left untouched when tracing is disabled, got %T (%v)", got, got)
		}
		if _, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); ok {
			t.Fatalf("expected no SDK TracerProvider to be installed when tracing is disabled")
		}
	})
}

// TestWithTracing_Enabled_InitializesSDKTracerProvider is the "flag on" case:
// when tracing.enabled is true, WithTracing must install a real SDK
// TracerProvider as the global provider, and a span created through it must
// be recorded.
func TestWithTracing_Enabled_InitializesSDKTracerProvider(t *testing.T) {
	withRestoredGlobalTracerProvider(t, func() {
		otel.SetTracerProvider(noop.NewTracerProvider())

		// Redirect stdout so the stdouttrace exporter's pretty-printed span
		// JSON doesn't pollute `go test` output.
		devNull, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatalf("failed to open %s: %v", os.DevNull, err)
		}
		defer func() { _ = devNull.Close() }()
		prevStdout := os.Stdout
		os.Stdout = devNull
		defer func() { os.Stdout = prevStdout }()

		a := &App{Cfg: &Config{Tracing: TracingConfig{Enabled: true}}}
		a.WithTracing()

		tp, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider)
		if !ok {
			t.Fatalf("expected a *sdktrace.TracerProvider to be installed when tracing is enabled, got %T", otel.GetTracerProvider())
		}
		defer func() { _ = tp.Shutdown(context.Background()) }()

		_, span := otel.Tracer("otel_test").Start(context.Background(), "test-span")
		if !span.IsRecording() {
			t.Fatalf("expected span to be recording when tracing is enabled")
		}
		if !span.SpanContext().IsValid() {
			t.Fatalf("expected span context to be valid when tracing is enabled")
		}
		span.End()
	})
}

// TestWithConfig_OtelTracingEnabledEnvVar_TakesPrecedenceOverConfigFile
// verifies the precedence the PR describes: the OTEL_TRACING_ENABLED
// environment variable wins over whatever tracing.enabled is set to in
// config.yaml, in both directions.
func TestWithConfig_OtelTracingEnabledEnvVar_TakesPrecedenceOverConfigFile(t *testing.T) {
	tests := []struct {
		name          string
		configEnabled bool
		envValue      string
		want          bool
	}{
		{name: "config true, env false wins", configEnabled: true, envValue: "false", want: false},
		{name: "config false, env true wins", configEnabled: false, envValue: "true", want: true},
		{name: "config true, no env set keeps config", configEnabled: true, envValue: "", want: true},
		{name: "config false, no env set keeps config", configEnabled: false, envValue: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			configsDir := filepath.Join(dir, "configs")
			if err := os.MkdirAll(configsDir, 0o755); err != nil {
				t.Fatalf("failed to create configs dir: %v", err)
			}
			yaml := "tracing:\n  enabled: " + boolStr(tt.configEnabled) + "\n"
			if err := os.WriteFile(filepath.Join(configsDir, "config.yaml"), []byte(yaml), 0o600); err != nil {
				t.Fatalf("failed to write config.yaml: %v", err)
			}

			t.Chdir(dir)
			if tt.envValue != "" {
				t.Setenv("OTEL_TRACING_ENABLED", tt.envValue)
			}

			a := &App{}
			a.WithConfig()

			if a.Cfg.Tracing.Enabled != tt.want {
				t.Fatalf("Cfg.Tracing.Enabled = %v, want %v", a.Cfg.Tracing.Enabled, tt.want)
			}
		})
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestOtelMuxMiddleware_EnabledConfig_ProducesSpanForRequest exercises the
// actual instrumented request path: when tracing is enabled, WithRoutes wires
// up the otelmux middleware, and a request through the resulting router must
// produce a recorded span.
func TestOtelMuxMiddleware_EnabledConfig_ProducesSpanForRequest(t *testing.T) {
	withRestoredGlobalTracerProvider(t, func() {
		var buf bytes.Buffer
		exporter, err := stdouttrace.New(stdouttrace.WithWriter(&buf))
		if err != nil {
			t.Fatalf("failed to create stdout exporter: %v", err)
		}
		tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
		otel.SetTracerProvider(tp)
		defer func() { _ = tp.Shutdown(context.Background()) }()

		a := &App{Cfg: &Config{Tracing: TracingConfig{Enabled: true}}}
		a.WithRoutes()
		// gorilla/mux only runs router middleware (incl. otelmux) for requests
		// that match a registered route, so register one for the test.
		a.e.HandleFunc("/some/instrumented/path", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}).Name("/some/instrumented/path")

		req := httptest.NewRequest(http.MethodGet, "/some/instrumented/path", nil)
		rec := httptest.NewRecorder()
		a.e.ServeHTTP(rec, req)

		if err := tp.ForceFlush(context.Background()); err != nil {
			t.Fatalf("failed to flush tracer provider: %v", err)
		}

		if buf.Len() == 0 {
			t.Fatalf("expected a span to be exported for the request, but no span data was captured")
		}
	})
}

// TestOtelMuxMiddleware_DisabledConfig_DoesNotInstrument confirms the flag-off
// path is a true no-op: no span is produced because otelmux middleware is
// never added to the router.
func TestOtelMuxMiddleware_DisabledConfig_DoesNotInstrument(t *testing.T) {
	withRestoredGlobalTracerProvider(t, func() {
		var buf bytes.Buffer
		exporter, err := stdouttrace.New(stdouttrace.WithWriter(&buf))
		if err != nil {
			t.Fatalf("failed to create stdout exporter: %v", err)
		}
		tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
		otel.SetTracerProvider(tp)
		defer func() { _ = tp.Shutdown(context.Background()) }()

		a := &App{Cfg: &Config{Tracing: TracingConfig{Enabled: false}}}
		a.WithRoutes()
		a.e.HandleFunc("/some/instrumented/path", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}).Name("/some/instrumented/path")

		req := httptest.NewRequest(http.MethodGet, "/some/instrumented/path", nil)
		rec := httptest.NewRecorder()
		a.e.ServeHTTP(rec, req)

		if err := tp.ForceFlush(context.Background()); err != nil {
			t.Fatalf("failed to flush tracer provider: %v", err)
		}

		if buf.Len() != 0 {
			t.Fatalf("expected no span to be exported when tracing is disabled, got: %s", buf.String())
		}
	})
}
