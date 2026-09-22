// Package oteljob implements optional job.Handler instrumentation with OTel.
// The caller delivers each completed Result exactly once. The application owns
// the MeterProvider, Resource, Views, exporters and shutdown. This package does
// not export telemetry, log errors or create traces itself.
//
// job.run.duration (seconds) has phase=total|work|error_handler and outcome.
// Count completed attempts using phase=total only. The error_handler phase is
// recorded only when called and describes the callback's own outcome; total
// and work retain the original work outcome. job.run.processed ({item}) records
// nonnegative reported counts, including zero and partial progress on failure,
// with the work outcome. Its sum is the total and its count the sample count.
//
// Results with zero Start are ignored because no work ran. Panics are visible
// only when recovered into Results; blocked work has no completion measurement.
// Unknown outcomes are normalized to "unknown" to bound attribute cardinality.
// Histogram boundaries are advisory and can be overridden with SDK Views.
package oteljob

import (
	"context"
	"errors"

	"github.com/uchaloop/job"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// MakeHandler builds optional instrumentation on an application-provided meter.
// It does not install itself on a runner or change the runner's behavior.
func MakeHandler(meter metric.Meter) (job.Handler, error) {
	if meter == nil {
		return nil, errors.New("meter is required")
	}

	duration, err := meter.Float64Histogram("job.run.duration",
		metric.WithDescription("Completed attempt duration by execution phase."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300, 600, 1800, 3600, 14400, 86400))
	if err != nil {
		return nil, err
	}

	processed, err := meter.Int64Histogram("job.run.processed",
		metric.WithDescription("Reported items per completed attempt, including partial progress."),
		metric.WithUnit("{item}"),
		metric.WithExplicitBucketBoundaries(0, 1, 10, 50, 100, 250, 500, 750, 1000, 1500, 2500, 5000, 10000))
	if err != nil {
		return nil, err
	}

	return &handler{duration: duration, processed: processed}, nil
}

type handler struct {
	duration  metric.Float64Histogram
	processed metric.Int64Histogram
}

func (h *handler) Handle(ctx context.Context, result job.Result) {
	if result.Start.IsZero() {
		return
	}

	outcome := attribute.String("outcome", normalizedOutcome(result.Outcome))
	h.duration.Record(ctx, result.Duration.Seconds(), metric.WithAttributes(outcome, attribute.String("phase", "total")))
	h.duration.Record(ctx, result.WorkDuration.Seconds(), metric.WithAttributes(outcome, attribute.String("phase", "work")))

	if result.ErrorHandlerCalled {
		h.duration.Record(ctx, result.ErrorHandlerDuration.Seconds(), metric.WithAttributes(
			attribute.String("outcome", errorOutcome(result.ErrorHandlerErr)), attribute.String("phase", "error_handler")))
	}

	if result.Processed >= 0 {
		h.processed.Record(ctx, result.Processed, metric.WithAttributes(outcome))
	}
}

func normalizedOutcome(outcome job.Outcome) string {
	switch outcome {
	case job.OutcomeOK, job.OutcomeError, job.OutcomePanic, job.OutcomeTimeout, job.OutcomeCanceled:
		return string(outcome)
	default:
		return "unknown"
	}
}

func errorOutcome(err error) string {
	if _, ok := errors.AsType[*job.PanicError](err); ok {
		return string(job.OutcomePanic)
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return string(job.OutcomeTimeout)
	case errors.Is(err, context.Canceled):
		return string(job.OutcomeCanceled)
	case err != nil:
		return string(job.OutcomeError)
	default:
		return string(job.OutcomeOK)
	}
}
