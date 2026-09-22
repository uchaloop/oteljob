# Job / CronJob dashboard

Import [job-cronjob.json](job-cronjob.json) into Grafana, then select your Prometheus
**Data source** and **Service**. Built-in panels only; tested with Grafana 13.2.2
and Prometheus 3.14.0. No local datasource UID or demo service is selected.

> This example targets last-run snapshots in Pushgateway. The current
> `oteljob.MakeHandler` emits cumulative execution histograms and does **not**
> publish the snapshot gauges below or push them to Pushgateway. A separate
> application-owned snapshot publisher is required. Importing the dashboard
> does not enable that publisher.

## Snapshot metric contract

| Prometheus metric | Type | Value |
|---|---|---|
| `job_last_run_status_ratio{outcome}` | Gauge | 0 for success; 1 for error, panic, timeout or canceled |
| `job_last_run_duration_seconds` | Gauge | Total duration of the latest attempt |
| `job_last_run_processed` | Gauge | Reported items in the latest attempt, including partial progress |
| `job_last_run_finished_at_seconds` | Gauge | Unix timestamp of the latest completion |
| `job_last_success_finished_at_seconds` | Gauge | Unix timestamp of the last known successful completion |
| `push_time_seconds` | Pushgateway gauge | Timestamp of the last successful group push |

The status metric's suffix comes from OTel unit `1`; it is a 0/1 result code,
not a measured error rate. Outcomes are `ok`, `error`, `panic`, `timeout`, `canceled`.

Groups must expose `service_name`, `deployment_environment_name` and a stable
logical `worker`. Optional cluster/namespace filters use `k8s_cluster_name` and
`k8s_namespace_name`. Do not generate a new worker label for every run. Independent
concurrent workers need distinct groups; overlapping runs in one group may
overwrite each other, including out-of-order delivery.

Publish all latest-attempt families together. On success also publish the
last-success timestamp. On failure leave that family untouched: Pushgateway
POST/Add replaces included families while preserving omitted ones; PUT replaces
the entire group. This also replaces old outcome labels within the status family.
Use a fresh bounded push context and configure Prometheus with honor_labels.

The panels show state at the selected end time, not totals over the selected
range. Do not apply rate/increase to these gauges or count repeated scrapes as
new attempts. Several runs between scrapes may be missed. Old values remain until
updated or deleted, so always consider completion/success age. No known success
is displayed as No data, not as zero seconds ago. Monitoring does not prove
exactly-once delivery or provide a complete execution audit.

## Optional Kubernetes block

The lower block reads kube-state-metrics for last scheduling, last successful
Job, active Jobs and observed active/complete/failed states. It needs:

- `kube_cronjob_info`, `kube_cronjob_status_last_schedule_time`,
  `kube_cronjob_status_last_successful_time`, `kube_cronjob_status_active`.
- `kube_job_owner`, `kube_job_status_active`, `kube_job_complete`, `kube_job_failed`.

Separate **K8s cluster / namespace / CronJob** filters avoid assuming a mapping
between service names and Kubernetes names. Multi-cluster collection requires a
stable `cluster` label on all kube-state-metrics series. HA copies are deduplicated
before the owner join. Without kube-state-metrics, the block has no data.

Job startup is not the start of job.Func; Pod retries may belong to one Job.
History includes only states Prometheus collected and is limited by retention.
Objects deleted before collection can be missing. Terminal-state bars may last
until object deletion; their width is not execution duration.

The link to Beat works when its dashboard is also imported with its original UID.
No application-start metric, execution-history store or alert policy is added.
