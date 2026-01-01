package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/tcaine/twamp"
)

type Output struct {
	Results any   `json:"results"`
	Stat    Stats `json:"stats"`
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

type twampSession struct {
	conn    *twamp.TwampConnection
	session *twamp.TwampSession
	test    *twamp.TwampTest
	mu      sync.Mutex
}

var (
	sessionCache = make(map[string]*twampSession)
	cacheMu      sync.Mutex
)

func getOrCreateSession(target string) (*twampSession, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	if s, ok := sessionCache[target]; ok {
		return s, nil
	}

	addr := fmt.Sprintf("%s:862", target)
	c := twamp.NewClient()

	conn, err := c.Connect(addr)
	if err != nil {
		return nil, err
	}

	session, err := conn.CreateSession(twamp.TwampSessionConfig{
		SenderPort:   6667,
		ReceiverPort: 6667,
		Timeout:      2,
		Padding:      42,
		TOS:          twamp.BE,
	})
	if err != nil {
		conn.Close()
		return nil, err
	}

	test, err := session.CreateTest()
	if err != nil {
		session.Stop()
		conn.Close()
		return nil, err
	}

	s := &twampSession{
		conn:    conn,
		session: session,
		test:    test,
	}

	sessionCache[target] = s
	log.Printf("Created persistent TWAMP session+test for %s", target)
	return s, nil
}

func deleteSession(target string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	if s, ok := sessionCache[target]; ok {
		s.session.Stop()
		s.conn.Close()
		delete(sessionCache, target)
		log.Printf("Deleted TWAMP session for %s", target)
	}
}

func shutdownAllSessions() {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	log.Println("Shutting down TWAMP sessions")

	for target, s := range sessionCache {
		log.Printf("Stopping TWAMP session for %s", target)
		s.session.Stop()
		s.conn.Close()
	}

	sessionCache = make(map[string]*twampSession)
}

func probeTWAMP(ctx context.Context, target string, registry *prometheus.Registry) bool {
	durationGaugeVec := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "twamp_duration_seconds",
			Help: "min/max/avg/stddev of twamp measurement",
		},
		[]string{"measurement"},
	)
	lostProbesGauge := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "twamp_probes_lost",
			Help: "Lost probes per measurement",
		},
	)

	registry.MustRegister(durationGaugeVec)
	registry.MustRegister(lostProbesGauge)

	s, err := getOrCreateSession(target)
	if err != nil {
		log.Printf("TWAMP session error for %s: %v", target, err)
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stop := make(chan bool)
	done := make(chan struct{})

	go func() {
		select {
		case <-ctx.Done():
			close(stop)
		case <-done:
		}
	}()

	results, err := s.test.RunMultiple(
		3,
		nil,
		time.Second,
		stop,
	)
	close(done)

	if err != nil {
		if err == io.EOF || strings.Contains(err.Error(), "EOF") {
			deleteSession(target)
		}
		log.Printf("RunMultiple failed for %s: %v", target, err)
		return false
	}

	var o Output
	if err := json.Unmarshal([]byte(s.test.ReturnJSON(results)), &o); err != nil {
		log.Printf("JSON parse failed: %v", err)
		return false
	}

	durationGaugeVec.WithLabelValues("min").Set(o.Stat.Min.Seconds())
	durationGaugeVec.WithLabelValues("max").Set(o.Stat.Max.Seconds())
	durationGaugeVec.WithLabelValues("avg").Set(o.Stat.Avg.Seconds())
	durationGaugeVec.WithLabelValues("stddev").Set(o.Stat.StdDev.Seconds())
	lostProbesGauge.Set(o.Stat.Loss)

	return true
}

func probeHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	target := r.URL.Query().Get("target")
	if target == "" {
		http.Error(w, "target parameter is required", http.StatusBadRequest)
		return
	}

	probeSuccess := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "probe_success",
		Help: "Displays whether or not the probe was successful",
	})
	probeDuration := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "probe_duration_seconds",
		Help: "Duration of the probe",
	})

	registry := prometheus.NewRegistry()
	registry.MustRegister(probeSuccess)
	registry.MustRegister(probeDuration)

	probeSuccess.Set(0)

	start := time.Now()
	if probeTWAMP(ctx, target, registry) {
		probeSuccess.Set(1)
	}
	probeDuration.Set(time.Since(start).Seconds())

	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(w, r)
}

func main() {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/probe", probeHandler)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `
<html>
<head><title>TWAMP Exporter</title></head>
<body>
<h1>TWAMP Exporter</h1>
<form action="/probe">
Target: <input name="target" value="192.168.100.1">
<input type="submit" value="Probe">
</form>
<p><a href="/metrics">Metrics</a></p>
</body>
</html>
`)
	})

	server := &http.Server{
		Addr:    ":9853",
		Handler: mux,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Println("Listening on :9853")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	<-stop
	log.Println("Shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)

	shutdownAllSessions()

	log.Println("Exporter shut down cleanly")
}
