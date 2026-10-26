package tracing

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// HTTPMiddleware wraps an http.Handler with OpenTelemetry HTTP instrumentation.
// It creates a span for each request and propagates trace context.
func HTTPMiddleware(next http.Handler) http.Handler {
	return otelhttp.NewHandler(next, ServiceName,
		otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
			return r.Method + " " + r.URL.Path
		}),
	)
}