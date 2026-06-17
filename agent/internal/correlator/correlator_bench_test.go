package correlator_test

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"testing"
	"time"

	"github.com/autosre/agent/internal/contracts"
	"github.com/autosre/agent/internal/correlator"
)

// BenchmarkCorrelator_10kSignals measures correlator throughput under a
// realistic storm of 10 000 signals spread across 100 distinct resources.
// It also asserts there is no goroutine leak after the correlator stops.
func BenchmarkCorrelator_10kSignals(b *testing.B) {
	log := slog.Default()

	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		cor := correlator.New(correlator.Config{
			CorrelationWindow: 5 * time.Second,
			ResolveWindow:     2 * time.Second,
			DedupWindow:       500 * time.Millisecond,
		}, log)

		src := make(chan contracts.Signal, 10_000)

		goroutinesBefore := runtime.NumGoroutine()

		// Start correlator in background; it will block until ctx is done.
		done := make(chan struct{})
		go func() {
			defer close(done)
			cor.Run(ctx, src)
		}()

		// Send 10 000 signals across 100 resources.
		const numSignals = 10_000
		const numResources = 100
		for j := 0; j < numSignals; j++ {
			src <- contracts.Signal{
				ID:        fmt.Sprintf("sig-%d-%d", i, j),
				Source:    "bench",
				Namespace: "bench-ns",
				Resource:  fmt.Sprintf("deploy-%d", j%numResources),
				Severity:  "high",
				Kind:      "Pod",
				Reason:    "CrashLoopBackOff",
				Message:   "back-off restarting failed container",
			}
		}
		close(src)

		// Cancel context to trigger correlator shutdown.
		cancel()
		<-done

		// Goroutine leak check: allow up to 5 extra goroutines for GC / runtime noise.
		goroutinesAfter := runtime.NumGoroutine()
		leaked := goroutinesAfter - goroutinesBefore
		if leaked > 5 {
			b.Errorf("goroutine leak: before=%d after=%d leaked=%d",
				goroutinesBefore, goroutinesAfter, leaked)
		}
	}
}

// BenchmarkCorrelator_Throughput measures single-resource signal ingestion rate
// with allocations reported per signal.
func BenchmarkCorrelator_Throughput(b *testing.B) {
	log := slog.Default()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cor := correlator.New(correlator.Config{
		CorrelationWindow: 10 * time.Second,
		ResolveWindow:     5 * time.Second,
		DedupWindow:       100 * time.Millisecond,
	}, log)

	src := make(chan contracts.Signal, b.N+1)

	go cor.Run(ctx, src)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		src <- contracts.Signal{
			ID:        fmt.Sprintf("s-%d", i),
			Source:    "bench",
			Namespace: "prod",
			Resource:  "api-deployment",
			Severity:  "critical",
			Kind:      "Pod",
			Reason:    "OOMKilled",
			Message:   "container killed by OOM",
		}
	}

	cancel()
}

// BenchmarkCorrelator_ManyResources measures how the correlator scales when
// incidents span a very large number of unique resources simultaneously.
func BenchmarkCorrelator_ManyResources(b *testing.B) {
	log := slog.Default()

	for _, numResources := range []int{10, 100, 1000} {
		numResources := numResources
		b.Run(fmt.Sprintf("resources=%d", numResources), func(b *testing.B) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			cor := correlator.New(correlator.Config{
				CorrelationWindow: 30 * time.Second,
				ResolveWindow:     10 * time.Second,
				DedupWindow:       1 * time.Second,
			}, log)

			src := make(chan contracts.Signal, b.N*numResources+1)
			go cor.Run(ctx, src)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				for r := 0; r < numResources; r++ {
					src <- contracts.Signal{
						ID:        fmt.Sprintf("s-%d-%d", i, r),
						Source:    "bench",
						Namespace: "prod",
						Resource:  fmt.Sprintf("svc-%d", r),
						Severity:  "high",
						Kind:      "Pod",
						Reason:    "CrashLoopBackOff",
					}
				}
			}
		})
	}
}
