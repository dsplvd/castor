package telemetry_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/stupside/castor/internal/telemetry"
	"github.com/stupside/castor/internal/transport"
)

func TestRPCTraceFollowsUnaryAndStreamingCalls(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) })
	ctx, root := provider.Tracer("test").Start(t.Context(), "request")
	defer root.End()
	var parent trace.SpanContext
	mux := http.NewServeMux()
	mux.Handle("/test.Service/Unary", connect.NewUnaryHandler("/test.Service/Unary", func(ctx context.Context, _ *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
		parent = trace.SpanContextFromContext(ctx)
		return connect.NewResponse(&emptypb.Empty{}), nil
	}, transport.Options()))
	mux.Handle("/test.Service/Watch", connect.NewServerStreamHandler("/test.Service/Watch", func(_ context.Context, _ *connect.Request[emptypb.Empty], stream *connect.ServerStream[emptypb.Empty]) error {
		if err := stream.Send(&emptypb.Empty{}); err != nil {
			return err
		}
		return connect.NewError(connect.CodePermissionDenied, errors.New("private-message-not-an-attribute"))
	}, transport.Options()))
	server := httptest.NewTestServer(t, mux)
	unary := connect.NewClient[emptypb.Empty, emptypb.Empty](server.Client(), server.URL+"/test.Service/Unary", transport.Options())
	if _, err := unary.CallUnary(ctx, connect.NewRequest(&emptypb.Empty{})); err != nil {
		t.Fatal(err)
	}
	if parent.TraceID() != root.SpanContext().TraceID() || parent.SpanID() == root.SpanContext().SpanID() {
		t.Fatal("server did not receive the child trace context")
	}
	watch := connect.NewClient[emptypb.Empty, emptypb.Empty](server.Client(), server.URL+"/test.Service/Watch", transport.Options())
	stream, err := watch.CallServerStream(ctx, connect.NewRequest(&emptypb.Empty{}))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() {
		t.Fatal("stream lost its message", stream.Err())
	}
	if stream.Receive() || connect.CodeOf(stream.Err()) != connect.CodePermissionDenied {
		t.Fatal("stream lost its terminal error", stream.Err())
	}
	_ = stream.Close()
	spans := exporter.GetSpans()
	if len(spans) != 4 {
		t.Fatalf("RPC spans = %d, want client and server per call", len(spans))
	}
	for _, span := range spans {
		if span.SpanContext.TraceID() != root.SpanContext().TraceID() {
			t.Fatal("RPC changed trace identity")
		}
		if span.Parent.SpanID() == (trace.SpanID{}) {
			t.Fatal("RPC lost its parent")
		}
		if span.Status.Description == "private-message-not-an-attribute" {
			t.Fatal("private error entered trace")
		}
	}
}

func TestDetachedWorkKeepsTraceButUsesItsOwnersCancellation(t *testing.T) {
	incoming, cancelRequest := context.WithCancel(t.Context())
	id := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}})
	incoming = trace.ContextWithSpanContext(incoming, id)
	lifetime, cancelOwner := context.WithCancel(t.Context())
	defer cancelOwner()
	work := telemetry.WithTrace(lifetime, incoming)
	cancelRequest()
	if work.Err() != nil {
		t.Fatal("accepted work ended with its request")
	}
	if !trace.SpanContextFromContext(work).Equal(id) {
		t.Fatal("accepted work lost correlation")
	}
	cancelOwner()
	if !errors.Is(work.Err(), context.Canceled) {
		t.Fatal("owner cannot cancel its work")
	}
}
