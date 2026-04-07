package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestNewTraceProvider(t *testing.T) {
	ctx := context.Background()
	res, err := resource.New(ctx)
	if err != nil {
		t.Fatalf("failed to create resource: %v", err)
	}

	exp := tracetest.NewInMemoryExporter()
	sp := sdktrace.NewSimpleSpanProcessor(exp)

	provider := newTraceProvider(res, sp)
	if provider == nil {
		t.Fatal("expected non-nil TracerProvider")
	}
	provider.Shutdown(ctx)
}

func TestNewResource(t *testing.T) {
	t.Run("default service name", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "")
		ctx := context.Background()
		res, err := newResource(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res == nil {
			t.Fatal("expected non-nil resource")
		}
		attrs := res.String()
		if !strings.Contains(attrs, "ssh-honeypot") {
			t.Errorf("expected resource to contain 'ssh-honeypot', got: %s", attrs)
		}
	})

	t.Run("custom service name", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "my-custom-service")
		ctx := context.Background()
		res, err := newResource(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res == nil {
			t.Fatal("expected non-nil resource")
		}
		attrs := res.String()
		if !strings.Contains(attrs, "my-custom-service") {
			t.Errorf("expected resource to contain 'my-custom-service', got: %s", attrs)
		}
	})
}

func TestReportErr(t *testing.T) {
	t.Run("nil error produces no output", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)

		reportErr(nil, "should not appear")
		if buf.Len() > 0 {
			t.Errorf("expected no log output, got: %s", buf.String())
		}
	})

	t.Run("non-nil error logs message", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)

		reportErr(fmt.Errorf("boom"), "operation failed")
		output := buf.String()
		if !strings.Contains(output, "operation failed") {
			t.Errorf("expected log to contain 'operation failed', got: %s", output)
		}
		if !strings.Contains(output, "boom") {
			t.Errorf("expected log to contain 'boom', got: %s", output)
		}
	})
}
