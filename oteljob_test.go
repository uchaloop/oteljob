package oteljob_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/uchaloop/job"
	"github.com/uchaloop/job/middleware/recovery"
	"github.com/uchaloop/oteljob"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func collect(t *testing.T, results ...job.Result) map[string]metricdata.Metrics {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	h, err := oteljob.MakeHandler(provider.Meter("test"))
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		h.Handle(context.Background(), result)
	}
	var resource metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &resource); err != nil {
		t.Fatal(err)
	}
	metrics := make(map[string]metricdata.Metrics)
	for _, scope := range resource.ScopeMetrics {
		for _, m := range scope.Metrics {
			metrics[m.Name] = m
		}
	}
	return metrics
}

func TestPhasesAndPartialProgress(t *testing.T) {
	metrics := collect(t, job.Result{
		Start: time.Now(), Duration: 3 * time.Second, WorkDuration: time.Second,
		ErrorHandlerCalled: true, ErrorHandlerDuration: 2 * time.Second,
		Err: errors.New("work"), Outcome: job.OutcomeError, Processed: 300,
	})
	if len(metrics) != 2 {
		t.Fatalf("instruments: %v", metrics)
	}
	duration := metrics["job.run.duration"].Data.(metricdata.Histogram[float64])
	want := map[string]struct {
		sum     float64
		outcome string
	}{"total": {3, "error"}, "work": {1, "error"}, "error_handler": {2, "ok"}}
	if len(duration.DataPoints) != len(want) {
		t.Fatal(duration)
	}
	for _, point := range duration.DataPoints {
		phase, _ := point.Attributes.Value("phase")
		outcome, _ := point.Attributes.Value("outcome")
		expected, ok := want[phase.AsString()]
		if !ok || point.Count != 1 || point.Sum != expected.sum || outcome.AsString() != expected.outcome || point.Attributes.Len() != 2 {
			t.Fatalf("point: %+v", point)
		}
	}
	processed := metrics["job.run.processed"].Data.(metricdata.Histogram[int64]).DataPoints
	if len(processed) != 1 || processed[0].Sum != 300 || processed[0].Count != 1 || processed[0].Attributes.Len() != 1 {
		t.Fatal(processed)
	}
}

func TestSkippedAndInvalidCounts(t *testing.T) {
	if got := collect(t, job.Result{Outcome: job.OutcomeCanceled, Err: context.Canceled}); len(got) != 0 {
		t.Fatal(got)
	}
	metrics := collect(t, job.Result{Start: time.Now(), Outcome: job.OutcomeOK, Processed: -1})
	if _, ok := metrics["job.run.processed"]; ok {
		t.Fatal("negative count recorded")
	}
	if _, ok := metrics["job.run.duration"]; !ok {
		t.Fatal("valid timing lost")
	}
}

func TestBucketsAndCounts(t *testing.T) {
	metrics := collect(t,
		job.Result{Start: time.Now(), Outcome: job.OutcomeOK, Processed: 0, Duration: 5 * time.Millisecond},
		job.Result{Start: time.Now(), Outcome: job.OutcomeOK, Processed: 1000, Duration: 3 * time.Second},
		job.Result{Start: time.Now(), Outcome: job.OutcomeOK, Processed: 1 << 40, Duration: 4 * time.Hour},
	)
	items := metrics["job.run.processed"].Data.(metricdata.Histogram[int64]).DataPoints[0]
	if items.Count != 3 || items.Sum != 1000+1<<40 || items.Bounds[0] != 0 || items.BucketCounts[0] != 1 {
		t.Fatal(items)
	}
	nonempty := 0
	for _, count := range items.BucketCounts {
		if count > 0 {
			nonempty++
		}
	}
	if nonempty != 3 {
		t.Fatalf("items collapsed into %d buckets", nonempty)
	}
	for _, point := range metrics["job.run.duration"].Data.(metricdata.Histogram[float64]).DataPoints {
		phase, _ := point.Attributes.Value("phase")
		if phase.AsString() != "total" {
			continue
		}
		nonempty = 0
		for _, count := range point.BucketCounts {
			if count > 0 {
				nonempty++
			}
		}
		if point.Count != 3 || nonempty != 3 {
			t.Fatal(point)
		}
	}
}

func TestCallbackOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"success", nil, "ok"}, {"error", errors.New("delivery"), "error"},
		{"timeout", errors.Join(errors.New("delivery"), context.DeadlineExceeded), "timeout"},
		{"cancel", context.Canceled, "canceled"}, {"panic", &job.PanicError{Value: "panic"}, "panic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metrics := collect(t, job.Result{Start: time.Now(), Outcome: job.OutcomeError, ErrorHandlerCalled: true, ErrorHandlerErr: tc.err})
			found := false
			for _, point := range metrics["job.run.duration"].Data.(metricdata.Histogram[float64]).DataPoints {
				phase, _ := point.Attributes.Value("phase")
				if phase.AsString() != "error_handler" {
					continue
				}
				outcome, _ := point.Attributes.Value("outcome")
				if outcome.AsString() != tc.want || point.Count != 1 || point.Sum != 0 {
					t.Fatal(point)
				}
				found = true
			}
			if !found {
				t.Fatal("zero-duration callback lost")
			}
		})
	}
}

func TestOutcomesAreBounded(t *testing.T) {
	var results []job.Result
	for _, outcome := range []job.Outcome{job.OutcomeOK, job.OutcomeError, job.OutcomePanic, job.OutcomeTimeout, job.OutcomeCanceled, "", "arbitrary-user-value"} {
		results = append(results, job.Result{Start: time.Now(), Outcome: outcome})
	}
	metrics := collect(t, results...)
	points := metrics["job.run.processed"].Data.(metricdata.Histogram[int64]).DataPoints
	if len(points) != 6 {
		t.Fatal(points)
	}
	for _, p := range points {
		outcome, _ := p.Attributes.Value("outcome")
		if outcome.AsString() == "unknown" && p.Count != 2 {
			t.Fatal(p)
		}
	}
}

func TestRunnerIntegration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner, err := job.MakeRunner(job.Config{Timeout: time.Second, ErrorHandlerTimeout: time.Second},
			func(ctx context.Context) (int64, error) { <-ctx.Done(); return 7, ctx.Err() },
			job.WithErrorHandler(func(ctx context.Context, _ error) error { <-ctx.Done(); return nil }))
		if err != nil {
			t.Fatal(err)
		}
		metrics := collect(t, runner.Run(context.Background()))
		for _, p := range metrics["job.run.duration"].Data.(metricdata.Histogram[float64]).DataPoints {
			outcome, _ := p.Attributes.Value("outcome")
			if outcome.AsString() != "timeout" {
				t.Fatal(p)
			}
		}
		runner, err = job.MakeRunner(job.Config{}, func(context.Context) (int64, error) { panic("boom") }, job.WithMiddleware(recovery.Middleware()))
		if err != nil {
			t.Fatal(err)
		}
		metrics = collect(t, runner.Run(context.Background()))
		for _, p := range metrics["job.run.duration"].Data.(metricdata.Histogram[float64]).DataPoints {
			outcome, _ := p.Attributes.Value("outcome")
			if outcome.AsString() != "panic" {
				t.Fatal(p)
			}
		}
	})
}

func TestViewOverridesBoundaries(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithView(sdkmetric.NewView(
		sdkmetric.Instrument{Name: "job.run.duration"}, sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: []float64{1, 10}}})))
	defer provider.Shutdown(context.Background())
	h, err := oteljob.MakeHandler(provider.Meter("test"))
	if err != nil {
		t.Fatal(err)
	}
	h.Handle(context.Background(), job.Result{Start: time.Now(), Outcome: job.OutcomeOK, Duration: 3 * time.Second})
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	for _, m := range data.ScopeMetrics[0].Metrics {
		if m.Name != "job.run.duration" {
			continue
		}
		for _, point := range m.Data.(metricdata.Histogram[float64]).DataPoints {
			if len(point.Bounds) != 2 || point.Bounds[0] != 1 || point.Bounds[1] != 10 {
				t.Fatal(point.Bounds)
			}
			if point.Attributes.HasValue(attribute.Key("service.name")) {
				t.Fatal("library injected resource identity")
			}
		}
	}
}

func TestNilMeter(t *testing.T) {
	if _, err := oteljob.MakeHandler(nil); err == nil {
		t.Fatal("nil accepted")
	}
}
