package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// captureLog redirects the global zerolog logger into buf for the duration of fn,
// then restores the previous logger.
func captureLog(t *testing.T, buf *bytes.Buffer, fn func()) {
	t.Helper()
	prev := log.Logger
	prevLevel := zerolog.GlobalLevel()
	log.Logger = zerolog.New(buf).Level(zerolog.TraceLevel)
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	defer func() {
		log.Logger = prev
		zerolog.SetGlobalLevel(prevLevel)
	}()
	fn()
}

func newSensitiveRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/some/path", strings.NewReader("body-with-secret-payload"))
	r.Header.Set("Authorization", "Bearer secret123")
	r.Header.Set("X-Plugin-Id", "plugin-secret-id")
	r.Header.Set("X-Id-Token", "id-token-secret")
	return r
}

// TestLogRequestData_NoTokenLogging_RedactsSensitiveHeaders is the red test:
// with log_tokens == false (the default), none of Authorization, X-Plugin-Id
// or X-Id-Token may ever reach the log output.
func TestLogRequestData_NoTokenLogging_RedactsSensitiveHeaders(t *testing.T) {
	var buf bytes.Buffer
	r := newSensitiveRequest()

	captureLog(t, &buf, func() {
		logRequestData(r, []byte("[REDACTED]"), false)
	})

	out := buf.String()
	for _, secret := range []string{"secret123", "plugin-secret-id", "id-token-secret", "Authorization", "X-Plugin-Id", "X-Id-Token"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log output leaked sensitive data %q; full output: %s", secret, out)
		}
	}
}

// TestLogRequestData_TokenLoggingEnabled_KeepsHeaders ensures the opt-in debug
// path still logs headers verbatim when the operator explicitly enabled it.
func TestLogRequestData_TokenLoggingEnabled_KeepsHeaders(t *testing.T) {
	var buf bytes.Buffer
	r := newSensitiveRequest()

	captureLog(t, &buf, func() {
		logRequestData(r, []byte("body"), true)
	})

	out := buf.String()
	if !strings.Contains(out, "secret123") {
		t.Fatalf("expected token to be present when log_tokens is true, got: %s", out)
	}
}

func TestCleanSensitiveHeaders_RemovesKnownHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer secret123")
	h.Set("X-Plugin-Id", "plugin-secret-id")
	h.Set("X-Id-Token", "id-token-secret")
	h.Set("X-Other", "keep-me")

	cleaned := cleanSensitiveHeaders(h)

	if cleaned.Get("Authorization") != "" || cleaned.Get("X-Plugin-Id") != "" || cleaned.Get("X-Id-Token") != "" {
		t.Fatalf("expected sensitive headers to be removed, got: %v", cleaned)
	}
	if cleaned.Get("X-Other") != "keep-me" {
		t.Fatalf("expected non-sensitive headers to be preserved, got: %v", cleaned)
	}
	// original headers must not be mutated
	if h.Get("Authorization") == "" {
		t.Fatalf("cleanSensitiveHeaders must not mutate the original headers")
	}
}

func TestCleanSensitiveHeaders_CaseInsensitive(t *testing.T) {
	h := http.Header{}
	// http.Header canonicalizes keys on Set, mirroring real request parsing.
	h.Set("authorization", "Bearer secret123")
	h.Set("x-plugin-id", "plugin-secret-id")
	h.Set("x-id-token", "id-token-secret")

	cleaned := cleanSensitiveHeaders(h)

	if cleaned.Get("Authorization") != "" || cleaned.Get("X-Plugin-Id") != "" || cleaned.Get("X-Id-Token") != "" {
		t.Fatalf("expected sensitive headers to be removed regardless of case, got: %v", cleaned)
	}
}

func TestReadBody_ReturnsBytesAndResetsBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello-body"))

	got := readBody(r)
	if string(got) != "hello-body" {
		t.Fatalf("expected body %q, got %q", "hello-body", string(got))
	}

	// body must be readable again by downstream handlers
	again, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("unexpected error re-reading body: %v", err)
	}
	if string(again) != "hello-body" {
		t.Fatalf("expected body to be reset for re-read, got %q", string(again))
	}
}

// TestLoggingMiddleware_AlertTokenHeaderRedacted ensures that with log_tokens == false
// the configured alert token header value never reaches the log, even though it is
// not part of the static deny list.
func TestLoggingMiddleware_AlertTokenHeaderRedacted(t *testing.T) {
	const alertSecret = "alert-secret-token-value"
	app := &App{Cfg: &Config{
		Log:   LogConfig{LogTokens: false},
		Alert: AlertConfig{Enabled: true, TokenHeader: "X-Multena-Alert"},
	}}
	mw := app.loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	r := httptest.NewRequest(http.MethodPost, "/api/v1/query", strings.NewReader("body"))
	r.Header.Set("X-Multena-Alert", "Bearer "+alertSecret)

	var buf bytes.Buffer
	captureLog(t, &buf, func() {
		mw.ServeHTTP(httptest.NewRecorder(), r)
	})

	out := buf.String()
	if !strings.Contains(out, "\"request\"") {
		t.Fatalf("expected request to be logged, got: %s", out)
	}
	if strings.Contains(out, alertSecret) {
		t.Fatalf("log output leaked alert token header value; full output: %s", out)
	}
}

// TestLogRequestData_ExtraSensitiveHeaderRedacted covers the direct helper path with
// an explicitly passed header name, matched case-insensitively.
func TestLogRequestData_ExtraSensitiveHeaderRedacted(t *testing.T) {
	var buf bytes.Buffer
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Multena-Alert-Token", "Bearer alert-leak-canary")
	r.Header.Set("X-Other", "keep-me")

	captureLog(t, &buf, func() {
		logRequestData(r, []byte("[REDACTED]"), false, "x-multena-alert-token", "")
	})

	out := buf.String()
	if strings.Contains(out, "alert-leak-canary") || strings.Contains(strings.ToLower(out), "x-multena-alert-token") {
		t.Fatalf("log output leaked extra sensitive header; full output: %s", out)
	}
	if !strings.Contains(out, "keep-me") {
		t.Fatalf("expected non-sensitive header to be preserved, got: %s", out)
	}
}

// TestLogRequestData_ExtraSensitiveHeaderKeptWhenTokenLoggingEnabled confirms the
// opt-in path still logs everything verbatim.
func TestLogRequestData_ExtraSensitiveHeaderKeptWhenTokenLoggingEnabled(t *testing.T) {
	var buf bytes.Buffer
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Multena-Alert", "Bearer alert-visible-value")

	captureLog(t, &buf, func() {
		logRequestData(r, []byte("body"), true, "X-Multena-Alert")
	})

	if !strings.Contains(buf.String(), "alert-visible-value") {
		t.Fatalf("expected alert header to be logged when log_tokens is true, got: %s", buf.String())
	}
}

// TestGetToken_DoesNotLogTokenValue guards against the bearer token being written to
// the trace log, which previously happened regardless of log_tokens.
func TestGetToken_DoesNotLogTokenValue(t *testing.T) {
	const canary = "leak-canary-token"
	app := &App{Cfg: &Config{Log: LogConfig{LogTokens: false}}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	// Two "Bearer" occurrences make parsing fail before any JWKS lookup happens.
	r.Header.Set("Authorization", "Bearer "+canary+" Bearer x")

	var buf bytes.Buffer
	captureLog(t, &buf, func() {
		_, _ = getToken(r, app)
	})

	if strings.Contains(buf.String(), canary) {
		t.Fatalf("token value leaked into trace log; full output: %s", buf.String())
	}
}
