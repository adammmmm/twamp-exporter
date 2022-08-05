# TWAMP Exporter

Took code from blackbox-exporter and snmp-exporter and adapted it to https://github.com/tcaine/twamp
Exporter will listen to /metrics for exporter level metrics and /probe for multi-target on :9853

Example prometheus configuration:
```
scrape_configs:
  - job_name: "twamp"
    metrics_path: /probe
    static_configs:
      - targets:
        - 192.168.100.1
        - 172.30.0.1
    relabel_configs:
      - source_labels: [__address__]
        target_label: __param_target
      - source_labels: [__param_target]
        target_label: instance
      - target_label: __address__
        replacement: 127.0.0.1:9853
```

Exported Metrics:
```
# HELP probe_duration_seconds Returns how long the probe took to complete in seconds
# TYPE probe_duration_seconds gauge
probe_duration_seconds 1.046873918
# HELP probe_success Displays whether or not the probe was a success
# TYPE probe_success gauge
probe_success 1
# HELP twamp_duration_seconds min/max/avg/stddev of twamp measurement
# TYPE twamp_duration_seconds gauge
twamp_duration_seconds{measurement="avg"} 0.004596
twamp_duration_seconds{measurement="max"} 0.006882
twamp_duration_seconds{measurement="min"} 0.003406
twamp_duration_seconds{measurement="stddev"} 0.001980291
# HELP twamp_probes_lost Lost probes per measurement
# TYPE twamp_probes_lost gauge
twamp_probes_lost 0
```
