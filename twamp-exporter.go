package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/adammmmm/twamp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Output struct {
	Results interface{} `json:"results"`
	Stat    Stats       `json:"stats"`
}

type Stats struct {
	Min         time.Duration `json:"min"`
	Max         time.Duration `json:"max"`
	Avg         time.Duration `json:"avg"`
	StdDev      time.Duration `json:"stddev"`
	Transmitted int           `json:"tx"`
	Received    int           `json:"rx"`
	Loss        float64       `json:"loss"`
}

func probeTWAMP(ctx context.Context, target string, registry *prometheus.Registry) (success bool) {
	var (
		durationGaugeVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "twamp_duration_seconds",
			Help: "min/max/avg/stddev of twamp measurement",
		}, []string{"measurement"})

		lostProbesGauge = prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "twamp_probes_lost",
			Help: "Lost probes per measurement",
		})
	)
	for _, lv := range []string{"min", "max", "avg", "stddev"} {
		durationGaugeVec.WithLabelValues(lv)
	}
	registry.MustRegister(durationGaugeVec)
	registry.MustRegister(lostProbesGauge)

	c := twamp.NewClient()
	ip := fmt.Sprintf("%s:862", target)
	connection, err := c.Connect(ip)
	if err != nil {
		return
	}

	session, err := connection.CreateSession(
		twamp.TwampSessionConfig{
			Port:    6666,
			Timeout: 1,
			Padding: 42,
			TOS:     twamp.BE,
		})
	if err != nil {
		return
	}

	test, err := session.CreateTest()
	if err != nil {
		return
	}

	results := test.RunX(3)
	output := test.ReturnJSON(results)
	jsonOutput := []byte(output)

	var o Output
	error := json.Unmarshal(jsonOutput, &o)
	if error != nil {
		return
	}

	defer session.Stop()
	defer connection.Close()

	durationGaugeVec.WithLabelValues("min").Add(float64(o.Stat.Min.Seconds()))
	durationGaugeVec.WithLabelValues("max").Add(float64(o.Stat.Max.Seconds()))
	durationGaugeVec.WithLabelValues("avg").Add(float64(o.Stat.Avg.Seconds()))
	durationGaugeVec.WithLabelValues("stddev").Add(float64(o.Stat.StdDev.Seconds()))
	lostProbesGauge.Set(o.Stat.Loss)
	return true
}

func probeHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(float64(time.Second*5)))
	r = r.WithContext(ctx)

	probeSuccessGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "probe_success",
		Help: "Displays whether or not the probe was a success",
	})
	probeDurationGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "probe_duration_seconds",
		Help: "Returns how long the probe took to complete in seconds",
	})
	defer cancel()
	params := r.URL.Query()

	target := params.Get("target")
	if target == "" {
		http.Error(w, "Target parameter is missing", http.StatusBadRequest)
		return
	}

	start := time.Now()
	registry := prometheus.NewRegistry()
	registry.MustRegister(probeSuccessGauge)
	registry.MustRegister(probeDurationGauge)
	success := probeTWAMP(ctx, target, registry)
	duration := time.Since(start).Seconds()
	probeDurationGauge.Set(duration)
	if success {
		probeSuccessGauge.Set(1)
	}

	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
}

func main() {
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/probe", func(w http.ResponseWriter, r *http.Request) {
		probeHandler(w, r)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
            <head>
            <title>TWAMP Exporter</title>
            <style>
            label{
            display:inline-block;
            width:75px;
            }
            form label {
            margin: 10px;
            }
            form input {
            margin: 10px;
            }
            </style>
            </head>
            <body>
            <h1>TWAMP Exporter</h1>
            <form action="/probe">
            <label>Target:</label> <input type="text" name="target" placeholder="X.X.X.X" value="1.2.3.4"><br>
            <input type="submit" value="Submit">
            </form>
						<p><a href="/metrics">Metrics</a></p>
            </body>
            </html>`))
	})
	log.Print("Listening on :9853")
	log.Fatal(http.ListenAndServe(":9853", nil))
}
