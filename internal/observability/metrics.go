package observability

import (
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/grafana/pyroscope-go"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Dashboard: rate(futureco_http_requests_total[5m])
// Dashboard: sum by (status) (rate(futureco_http_requests_total[5m]))
// Dashboard: sum by (route) (rate(futureco_http_requests_total[5m]))
// SLI: 1 - (sum(rate(futureco_http_requests_total{status=~"5.."}[5m])) / sum(rate(futureco_http_requests_total[5m])))
// Alert: sum(rate(futureco_http_requests_total{status=~"5.."}[5m])) / sum(rate(futureco_http_requests_total[5m])) > 0.01.
var httpRequestsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "futureco_http_requests_total",
		Help: "HTTP requests processed, partitioned by method, route, and status.",
	},
	[]string{"method", "route", "status"},
)

// Dashboard: histogram_quantile(0.99, sum by (le) (rate(futureco_http_request_duration_seconds_bucket[5m])))
// Dashboard: histogram_quantile(0.99, sum by (le, route) (rate(futureco_http_request_duration_seconds_bucket[5m])))
// Alert: histogram_quantile(0.99, sum by (le) (rate(futureco_http_request_duration_seconds_bucket[5m]))) > 2.
var httpRequestDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "futureco_http_request_duration_seconds",
		Help:    "HTTP request latency in seconds.",
		Buckets: prometheus.DefBuckets,
	},
	[]string{"method", "route"},
)

// Instrument records latency and status for route, which must be the mux pattern and not the raw path.
// A failed request is logged once here. Handlers return errors and do not log them again.
func Instrument(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		elapsed := time.Since(start).Seconds()
		status := strconv.Itoa(rec.status)
		httpRequestsTotal.WithLabelValues(r.Method, route, status).Inc()
		httpRequestDuration.WithLabelValues(r.Method, route).Observe(elapsed)

		if rec.status >= http.StatusInternalServerError {
			slog.ErrorContext(r.Context(), "request failed",
				"method", r.Method,
				"route", route,
				"status", rec.status,
				"duration_seconds", elapsed,
			)

			return
		}

		slog.InfoContext(r.Context(), "request",
			"method", r.Method,
			"route", route,
			"status", rec.status,
			"duration_seconds", elapsed,
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Dashboard: futureco_db_pool_acquired_connections
// Dashboard: futureco_db_pool_acquired_connections / futureco_db_pool_max_connections
// Alert: futureco_db_pool_acquired_connections / futureco_db_pool_max_connections > 0.9

// RegisterPoolMetrics exports the pgx pool gauges used by those queries.
func RegisterPoolMetrics(pool *pgxpool.Pool) {
	prometheus.MustRegister(newPoolCollector(pool))
}

type poolCollector struct {
	pool     *pgxpool.Pool
	acquired *prometheus.Desc
	idle     *prometheus.Desc
	max      *prometheus.Desc
	total    *prometheus.Desc
}

func newPoolCollector(pool *pgxpool.Pool) *poolCollector {
	return &poolCollector{
		pool: pool,
		acquired: prometheus.NewDesc(
			"futureco_db_pool_acquired_connections",
			"Connections currently acquired from the pool.",
			nil, nil,
		),
		idle: prometheus.NewDesc(
			"futureco_db_pool_idle_connections",
			"Idle connections in the pool.",
			nil, nil,
		),
		max: prometheus.NewDesc(
			"futureco_db_pool_max_connections",
			"Configured maximum connections for this process.",
			nil, nil,
		),
		total: prometheus.NewDesc(
			"futureco_db_pool_total_connections",
			"Total connections currently in the pool.",
			nil, nil,
		),
	}
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.acquired

	ch <- c.idle

	ch <- c.max

	ch <- c.total
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	stat := c.pool.Stat()
	ch <- prometheus.MustNewConstMetric(c.acquired, prometheus.GaugeValue, float64(stat.AcquiredConns()))

	ch <- prometheus.MustNewConstMetric(c.idle, prometheus.GaugeValue, float64(stat.IdleConns()))

	ch <- prometheus.MustNewConstMetric(c.max, prometheus.GaugeValue, float64(stat.MaxConns()))

	ch <- prometheus.MustNewConstMetric(c.total, prometheus.GaugeValue, float64(stat.TotalConns()))
}

// SetupProfiling starts Pyroscope when PROFILING_ENABLED=true.
// The address is not logged, because PYROSCOPE_URL may contain credentials.
// pprof is not mounted on the public mux.
func SetupProfiling() {
	if os.Getenv("PROFILING_ENABLED") != "true" {
		return
	}

	_, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: "futureco-schedule",
		ServerAddress:   os.Getenv("PYROSCOPE_URL"),
		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,
		},
	})
	if err != nil {
		slog.Error("failed to start pyroscope", "error", err)
		return
	}

	slog.Info("continuous profiling enabled")
}
