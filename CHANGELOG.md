# Changelog

## [0.1.0] - 2026-09-22

Initial release of the optional OpenTelemetry adapter for job.

### Added

- Document optional Fx integration through the independent `oteljobfx` module.

- `MakeHandler(metric.Meter)` returning a `job.Handler`. The application owns
  result delivery, SDK configuration, Resource attributes, exporters and shutdown.
- `job.run.duration`: histogram of work, error processing and total duration,
  with bounded phase/outcome attributes and independent error-handler outcomes.
- `job.run.processed`: `int64` batch-size histogram including empty batches and
  partial progress on failure. Negative reported counts are omitted.
- Ignore results whose work never started, and normalize unknown outcomes.
- Provide explicit histogram boundaries overridable with SDK Views, an
  executable example, SDK-backed tests and CI.
- Include an English Job / CronJob Grafana example with a Pushgateway snapshot
  contract and an optional kube-state-metrics lifecycle/history block. Snapshot
  publishing is application-owned and is not provided by `MakeHandler`.

[0.1.0]: https://github.com/uchaloop/oteljob/releases/tag/v0.1.0
