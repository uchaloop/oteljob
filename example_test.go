package oteljob_test

import (
	"context"
	"fmt"
	"time"

	"github.com/uchaloop/job"
	"github.com/uchaloop/oteljob"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func ExampleMakeHandler() {
	// Use a configured exporter with a PeriodicReader in a one-shot application.
	// This example uses a ManualReader to inspect measurements without a server.
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	observer, err := oteljob.MakeHandler(provider.Meter("example/worker"))
	if err != nil {
		panic(err)
	}
	runner, err := job.MakeRunner(job.Config{Timeout: time.Minute},
		func(context.Context) (int64, error) { return 1000, nil })
	if err != nil {
		panic(err)
	}
	result := runner.Run(context.Background())
	observer.Handle(context.Background(), result) // Explicit, exactly once.
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		panic(err)
	}
	fmt.Println("instruments:", len(data.ScopeMetrics[0].Metrics))

	// A production provider flushes its configured reader/exporter on shutdown.
	// Use a fresh budget even if the work context was canceled.
	flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := provider.Shutdown(flushCtx); err != nil {
		panic(err)
	}
	// Output: instruments: 2
}
