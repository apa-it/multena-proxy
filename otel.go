package main

import (
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// WithTracing initializes OpenTelemetry tracing when enabled via config
// (`tracing.enabled`) or the OTEL_TRACING_ENABLED environment variable.
//
// This is a minimal, flag-gated, first-step adoption of OTel tracing:
//   - stdout exporter only, no OTLP/Collector wiring.
//   - BatchSpanProcessor (not Simple) to keep overhead off the request path.
//
// When tracing is disabled (the default), no SDK components are initialized
// at all: the global tracer provider stays the OTel SDK's built-in no-op
// implementation, so there is no measurable runtime overhead.
func (a *App) WithTracing() *App {
	if !a.Cfg.Tracing.Enabled {
		log.Info().Msg("OTel tracing disabled")
		return a
	}

	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		log.Fatal().Err(err).Msg("Error while creating stdout trace exporter")
	}

	res := resource.NewSchemaless(attribute.String("service.name", "multena-proxy"))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	log.Warn().Msg("OTel tracing enabled (experimental, stdout exporter only, no production backend wiring)")
	return a
}
