package metrics

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Label names used by the instrumented metrics.
const (
	LabelMethod = "method"
	LabelRoute  = "route"
	LabelCode   = "code"
)

// Metric names.
const (
	RequestsTotalName   = "http_requests_total"
	RequestDurationName = "http_request_duration_seconds"
)

// DurationBuckets cover 0.5ms .. ~4s; exponential buckets keep relative
// percentile error roughly constant across the whole range.
var DurationBuckets = prometheus.ExponentialBuckets(0.0005, 2, 14)

// Handler describes an instrumented endpoint.
type Handler struct {
	Method string `json:"method"`
	Route  string `json:"route"`
}

// Router registers handlers on a ServeMux, wrapping each one with metrics.
// Its Handle method matches api.Router, so it can be used as a drop-in.
type Router struct {
	mux      *http.ServeMux
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec

	mu       sync.RWMutex
	handlers []Handler
}

// NewRouter creates metrics, registers them in reg and returns a router
// that adds instrumented handlers to mux.
func NewRouter(reg prometheus.Registerer, mux *http.ServeMux) *Router {
	r := &Router{
		mux: mux,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: RequestsTotalName,
			Help: "Total number of HTTP requests by handler and response code.",
		}, []string{LabelMethod, LabelRoute, LabelCode}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    RequestDurationName,
			Help:    "HTTP request latency in seconds by handler.",
			Buckets: DurationBuckets,
		}, []string{LabelMethod, LabelRoute}),
	}
	reg.MustRegister(r.requests, r.duration)
	return r
}

// Handle registers an instrumented handler. The pattern must have the
// "METHOD /path" form, e.g. "GET /items/{id}"; the path template becomes
// the route label, so label cardinality is bounded by the number of routes.
func (r *Router) Handle(pattern string, h http.Handler) {
	method, route, ok := strings.Cut(pattern, " ")
	if !ok || method == "" || !strings.HasPrefix(route, "/") {
		panic(fmt.Sprintf("metrics: pattern %q must have the form \"METHOD /path\"", pattern))
	}

	// Pre-create the latency series so the handler shows up with zero
	// values before the first request arrives.
	r.duration.WithLabelValues(method, route)

	r.mux.Handle(pattern, r.instrument(method, route, h))

	r.mu.Lock()
	r.handlers = append(r.handlers, Handler{Method: method, Route: route})
	r.mu.Unlock()
}

// Handlers returns a copy of the registered handlers in registration order.
func (r *Router) Handlers() []Handler {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Handler(nil), r.handlers...)
}

// HandlersHandler serves the list of instrumented handlers as JSON:
// {"handlers":[{"method":"GET","route":"/items/{id}"}, ...]}.
func (r *Router) HandlersHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string][]Handler{"handlers": r.Handlers()})
	})
}

func (r *Router) instrument(method, route string, next http.Handler) http.Handler {
	duration := r.duration.WithLabelValues(method, route)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, req)

		duration.Observe(time.Since(start).Seconds())
		r.requests.WithLabelValues(method, route, strconv.Itoa(rec.status)).Inc()
	})
}

// statusRecorder captures the response status code written by a handler.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
