// otlptest is a load-testing tool for OTLP gRPC trace endpoints.
// It simulates concurrent clients sending traces to an OTLP endpoint
// and reports context deadline exceeded / cancelled / other failures.
//
// Usage:
//
//	go run ./cmd/otlptest \
//	  -endpoint jaeger.jaeger.svc:4317 \
//	  -clients 20 \
//	  -spans-per-batch 500 \
//	  -duration 60s \
//	  -timeout 2s \
//	  -insecure
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stats struct {
	exported       atomic.Int64
	deadlineErrors atomic.Int64
	cancelErrors   atomic.Int64
	unavailErrors  atomic.Int64
	otherErrors    atomic.Int64
}

func main() {
	endpoint := flag.String("endpoint", "localhost:4317", "OTLP gRPC endpoint (host:port)")
	clients := flag.Int("clients", 10, "Number of concurrent exporter clients")
	spansPerBatch := flag.Int("spans-per-batch", 200, "Number of spans per flush batch")
	duration := flag.Duration("duration", 30*time.Second, "Total test duration")
	timeout := flag.Duration("timeout", 2*time.Second, "Export timeout per batch")
	insecure := flag.Bool("insecure", false, "Use insecure (plaintext) gRPC connection")
	printInterval := flag.Duration("print-interval", 5*time.Second, "How often to print stats")
	flag.Parse()

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "error: -endpoint is required")
		os.Exit(1)
	}

	log.Printf("OTLP Endpoint Test")
	log.Printf("  endpoint:        %s", *endpoint)
	log.Printf("  clients:         %d", *clients)
	log.Printf("  spans-per-batch: %d", *spansPerBatch)
	log.Printf("  duration:        %s", *duration)
	log.Printf("  timeout:         %s", *timeout)
	log.Printf("  insecure:        %v", *insecure)
	log.Println()

	var s stats

	// Log OTel SDK internal errors but don't count them — ForceFlush
	// already counts export failures, and the SDK error handler would
	// double-count them.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		// deliberately not counted
	}))

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	var wg sync.WaitGroup

	// Launch concurrent clients — each with its own gRPC connection.
	for i := range *clients {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			runClient(ctx, clientID, *endpoint, *insecure, *timeout, *spansPerBatch, &s)
		}(i)
	}

	// Print periodic stats.
	go func() {
		ticker := time.NewTicker(*printInterval)
		defer ticker.Stop()
		start := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				elapsed := time.Since(start).Truncate(time.Second)
				printStats(elapsed, &s)
			}
		}
	}()

	wg.Wait()
	log.Println()
	log.Println("=== FINAL RESULTS ===")
	printStats(*duration, &s)

	total := s.exported.Load() + s.deadlineErrors.Load() + s.cancelErrors.Load() + s.unavailErrors.Load() + s.otherErrors.Load()
	if total == 0 {
		log.Println("WARNING: no batches were sent")
		os.Exit(1)
	}

	failRate := float64(s.deadlineErrors.Load()+s.cancelErrors.Load()+s.unavailErrors.Load()+s.otherErrors.Load()) / float64(total) * 100
	log.Printf("Failure rate: %.2f%%", failRate)
	if s.deadlineErrors.Load() > 0 || s.cancelErrors.Load() > 0 {
		log.Println("RESULT: DEADLINE_EXCEEDED / CANCELLED errors detected — endpoint is failing under load")
		os.Exit(2)
	}
	log.Println("RESULT: All exports succeeded — endpoint is healthy")
}

func runClient(ctx context.Context, clientID int, endpoint string, insecure bool, timeout time.Duration, spansPerBatch int, s *stats) {
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithTimeout(timeout),
		otlptracegrpc.WithRetry(otlptracegrpc.RetryConfig{Enabled: false}), // No retries — we want to see raw failures.
	}
	if insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		log.Printf("[client %d] failed to create exporter: %v", clientID, err)
		return
	}
	defer exporter.Shutdown(context.Background()) //nolint:errcheck

	res, _ := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceName(fmt.Sprintf("otlptest-client-%d", clientID)),
	))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(exporter,
			sdktrace.WithMaxExportBatchSize(spansPerBatch),
			sdktrace.WithBatchTimeout(200*time.Millisecond),
			sdktrace.WithExportTimeout(timeout),
		),
	)
	defer tp.Shutdown(context.Background()) //nolint:errcheck

	tracer := tp.Tracer("otlptest")
	generateSpans(ctx, tracer, clientID, spansPerBatch, timeout, s, tp)
}

func generateSpans(ctx context.Context, tracer trace.Tracer, clientID int, spansPerBatch int, timeout time.Duration, s *stats, tp *sdktrace.TracerProvider) {
	batchNum := 0
	for ctx.Err() == nil {
		for j := range spansPerBatch {
			_, span := tracer.Start(ctx, fmt.Sprintf("test-span-%d", j))
			span.SetAttributes(
				attribute.Int("client.id", clientID),
				attribute.Int("span.index", j),
				attribute.Int("batch.num", batchNum),
				attribute.String("test.payload", strings.Repeat("x", 64)),
			)
			span.End()
		}

		// Force flush to trigger export.
		flushCtx, flushCancel := context.WithTimeout(ctx, timeout)
		err := tp.ForceFlush(flushCtx)
		flushCancel()

		if err != nil {
			classifyError(err, s)
		} else {
			s.exported.Add(1)
		}
		batchNum++
	}
}

func classifyError(err error, s *stats) {
	st, ok := status.FromError(err)
	if ok {
		switch st.Code() {
		case codes.DeadlineExceeded:
			s.deadlineErrors.Add(1)
			return
		case codes.Canceled:
			s.cancelErrors.Add(1)
			return
		case codes.Unavailable:
			s.unavailErrors.Add(1)
			return
		}
	}

	errMsg := err.Error()
	switch {
	case strings.Contains(errMsg, "deadline exceeded"):
		s.deadlineErrors.Add(1)
	case strings.Contains(errMsg, "canceled"):
		s.cancelErrors.Add(1)
	case strings.Contains(errMsg, "unavailable"):
		s.unavailErrors.Add(1)
	default:
		s.otherErrors.Add(1)
		log.Printf("  unclassified error: %v", err)
	}
}

func printStats(elapsed time.Duration, s *stats) {
	ok := s.exported.Load()
	deadline := s.deadlineErrors.Load()
	cancelled := s.cancelErrors.Load()
	unavail := s.unavailErrors.Load()
	other := s.otherErrors.Load()
	total := ok + deadline + cancelled + unavail + other

	log.Printf("[%s] batches: total=%d ok=%d deadline_exceeded=%d cancelled=%d unavailable=%d other=%d",
		elapsed, total, ok, deadline, cancelled, unavail, other)
}
