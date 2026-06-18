package telemetry

import (
	"github.com/gofiber/fiber/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// fiberHeaderCarrier adapts Fiber's request headers to the OTel
// TextMapCarrier interface so trace context can be extracted from
// incoming requests.
type fiberHeaderCarrier struct {
	c fiber.Ctx
}

func (f fiberHeaderCarrier) Get(key string) string {
	return f.c.Get(key)
}

func (f fiberHeaderCarrier) Set(key, value string) {
	f.c.Set(key, value)
}

func (f fiberHeaderCarrier) Keys() []string {
	headers := f.c.GetReqHeaders()
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	return keys
}

// FiberTracing returns a Fiber middleware that emits an OpenTelemetry server
// span for each request. It extracts the parent trace context from incoming
// headers, sets standard HTTP span attributes, and rewrites the span name to
// "METHOD ROUTE" once Fiber has matched the route. Requests whose path
// starts with one of skipPrefixes are passed through without tracing.
func FiberTracing(serviceName string, skipPrefixes []string) fiber.Handler {
	propagator := otel.GetTextMapPropagator()

	return func(c fiber.Ctx) error {
		path := c.Path()
		for _, prefix := range skipPrefixes {
			if len(prefix) > 0 && len(path) >= len(prefix) && path[:len(prefix)] == prefix {
				return c.Next()
			}
		}

		ctx := propagator.Extract(c.Context(), fiberHeaderCarrier{c: c})

		ctx, span := Tracer().Start(ctx, c.Method()+" "+path,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.HTTPRequestMethodKey.String(c.Method()),
				semconv.URLPath(path),
				semconv.URLScheme(c.Protocol()),
				semconv.ServerAddress(c.Hostname()),
				semconv.UserAgentOriginal(c.Get(fiber.HeaderUserAgent)),
			),
		)
		defer span.End()

		c.SetContext(ctx)

		err := c.Next()

		// Rewrite the span name to use the matched route pattern.
		if route := c.Route(); route != nil && route.Path != "" {
			span.SetName(c.Method() + " " + route.Path)
			span.SetAttributes(attribute.String("http.route", route.Path))
		}

		status := c.Response().StatusCode()
		span.SetAttributes(semconv.HTTPResponseStatusCode(status))

		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else if status >= 500 {
			span.SetStatus(codes.Error, "")
		}

		return err
	}
}

// Ensure fiberHeaderCarrier satisfies the propagation carrier interface at compile time.
var _ propagation.TextMapCarrier = fiberHeaderCarrier{}
