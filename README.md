# TWAMP Exporter

Took code from blackbox-exporter and snmp-exporter and adapted it to github.com/tcaine/twamp

Exporter will listen on :9853 and reply on /metrics for exporter level metrics and /probe?target=1.2.3.4 to run test to 1.2.3.4