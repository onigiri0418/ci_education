package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Server bundles dependencies for handlers.
type Server struct {
	httpClient *http.Client
	cache      *pokemonCache
	metrics    *metrics
	baseURL    string
}

// pokemonResponse is the response model returned by our API.
type pokemonResponse struct {
	Name           string `json:"name"`
	Height         int    `json:"height"`
	Weight         int    `json:"weight"`
	BaseExperience int    `json:"base_experience"`
}

// simple in-memory TTL cache
type cacheEntry struct {
	value     pokemonResponse
	expiresAt time.Time
}

type pokemonCache struct {
	mu   sync.RWMutex
	data map[string]cacheEntry
	ttl  time.Duration
}

func newPokemonCache(ttl time.Duration) *pokemonCache {
	return &pokemonCache{data: make(map[string]cacheEntry), ttl: ttl}
}

func (c *pokemonCache) get(key string) (pokemonResponse, bool) {
	c.mu.RLock()
	entry, ok := c.data[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			// cleanup expired
			c.mu.Lock()
			delete(c.data, key)
			c.mu.Unlock()
		}
		return pokemonResponse{}, false
	}
	return entry.value, true
}

func (c *pokemonCache) set(key string, value pokemonResponse) {
	c.mu.Lock()
	c.data[key] = cacheEntry{value: value, expiresAt: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}

// metrics setup
type metrics struct {
	requestsTotal      *prometheus.CounterVec
	requestDurationSec *prometheus.HistogramVec
	extCallsTotal      *prometheus.CounterVec
	extCallDurationSec *prometheus.HistogramVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	m := &metrics{
		requestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "http_requests_total", Help: "Total HTTP requests"},
			[]string{"route", "method", "status"},
		),
		requestDurationSec: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "http_request_duration_seconds", Help: "HTTP request duration", Buckets: prometheus.DefBuckets},
			[]string{"route", "method"},
		),
		extCallsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "external_api_requests_total", Help: "External API requests"},
			[]string{"target", "status"},
		),
		extCallDurationSec: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "external_api_request_duration_seconds", Help: "External API call duration", Buckets: prometheus.DefBuckets},
			[]string{"target"},
		),
	}
	reg.MustRegister(m.requestsTotal, m.requestDurationSec, m.extCallsTotal, m.extCallDurationSec)
	return m
}

// setupRouter configures routes and middleware.
func setupRouter(s *Server) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestIDMiddleware())
	r.Use(accessLogMiddleware(s))
	r.Use(metricsMiddleware(s))

	r.GET("/health", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	r.GET("/hello", func(c *gin.Context) {
		name := c.Query("name")
		if name == "" {
			name = "world"
		}
		c.JSON(http.StatusOK, gin.H{"message": "hello " + name})
	})

	r.GET("/pokemon/:name", func(c *gin.Context) {
		name := c.Param("name")
		if name == "" {
			writeError(c, http.StatusBadRequest, "bad_request", "name is required")
			return
		}

		// cache first
		if v, ok := s.cache.get(name); ok {
			c.JSON(http.StatusOK, v)
			return
		}

		p, status, err := s.fetchPokemon(c.Request.Context(), name)
		if err != nil {
			// normalize status and message
			if status == http.StatusNotFound {
				writeError(c, status, "not_found", "pokemon not found")
				return
			}
			writeError(c, status, "upstream_error", err.Error())
			return
		}
		s.cache.set(name, p)
		c.JSON(http.StatusOK, p)
	})

	// Prometheus metrics endpoint
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	return r
}

// HTTP fetch with timeout + retry + metrics
func (s *Server) fetchPokemon(ctx context.Context, name string) (pokemonResponse, int, error) {
	url := fmt.Sprintf("%s/pokemon/%s", s.baseURL, name)
	const target = "pokeapi"
	start := time.Now()
	defer func() {
		s.metrics.extCallDurationSec.WithLabelValues(target).Observe(time.Since(start).Seconds())
	}()

	var lastErr error
	maxAttempts := 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := s.httpClient.Do(req)
		if err != nil {
			// retry on temporary network errors
			if isRetryable(err) && attempt < maxAttempts {
				backoff(attempt)
				lastErr = err
				continue
			}
			s.metrics.extCallsTotal.WithLabelValues(target, "error").Inc()
			return pokemonResponse{}, http.StatusBadGateway, fmt.Errorf("failed to call upstream: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			var data pokemonResponse
			if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
				s.metrics.extCallsTotal.WithLabelValues(target, "parse_error").Inc()
				return pokemonResponse{}, http.StatusBadGateway, fmt.Errorf("failed to parse response: %w", err)
			}
			s.metrics.extCallsTotal.WithLabelValues(target, "200").Inc()
			return data, http.StatusOK, nil
		}

		if resp.StatusCode >= 500 && attempt < maxAttempts {
			// server error: retry
			backoff(attempt)
			lastErr = fmt.Errorf("upstream status %d", resp.StatusCode)
			continue
		}
		// non-retryable status
		s.metrics.extCallsTotal.WithLabelValues(target, strconv.Itoa(resp.StatusCode)).Inc()
		if resp.StatusCode == http.StatusNotFound {
			return pokemonResponse{}, http.StatusNotFound, errors.New("pokemon not found")
		}
		return pokemonResponse{}, http.StatusBadGateway, fmt.Errorf("upstream returned status %d", resp.StatusCode)
	}
	s.metrics.extCallsTotal.WithLabelValues(target, "error").Inc()
	return pokemonResponse{}, http.StatusBadGateway, fmt.Errorf("upstream retries exhausted: %v", lastErr)
}

func isRetryable(err error) bool {
	var nerr net.Error
	if errors.As(err, &nerr) {
		return nerr.Timeout() || nerr.Temporary()
	}
	return true // treat unknown transport errors as retryable
}

func backoff(attempt int) {
	// exponential backoff with jitter, base 100ms
	base := 100 * time.Millisecond
	max := 1 * time.Second
	d := time.Duration(float64(base) * math.Pow(2, float64(attempt-1)))
	if d > max {
		d = max
	}
	// small jitter
	time.Sleep(d - time.Duration(randByte()%30)*time.Millisecond)
}

func randByte() byte {
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0
	}
	return b[0]
}

// middleware: request ID
func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-ID")
		if rid == "" {
			rid = genRequestID()
		}
		c.Set("request_id", rid)
		c.Writer.Header().Set("X-Request-ID", rid)
		c.Next()
	}
}

func genRequestID() string {
	// 16 random bytes hex-encoded => 32 chars
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// fallback to timestamp
		return fmt.Sprintf("ts-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// middleware: access log (concise) + include request ID
func accessLogMiddleware(s *Server) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		rid, _ := c.Get("request_id")
		status := c.Writer.Status()
		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		log.Printf("rid=%v method=%s route=%s status=%d duration=%s", rid, c.Request.Method, route, status, time.Since(start))
	}
}

// middleware: record metrics per request
func metricsMiddleware(s *Server) gin.HandlerFunc {
	return func(c *gin.Context) {
		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		method := c.Request.Method
		start := time.Now()
		c.Next()
		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())
		s.metrics.requestsTotal.WithLabelValues(route, method, status).Inc()
		s.metrics.requestDurationSec.WithLabelValues(route, method).Observe(duration)
	}
}

// unified error writer
func writeError(c *gin.Context, code int, errCode, msg string) {
	rid, _ := c.Get("request_id")
	c.JSON(code, gin.H{
		"error": gin.H{
			"code":       errCode,
			"message":    msg,
			"request_id": rid,
		},
	})
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func main() {
	timeoutSec := getenvInt("HTTP_TIMEOUT_SEC", 5)
	cacheTTL := time.Duration(getenvInt("POKEMON_CACHE_TTL_SEC", 300)) * time.Second

	s := &Server{
		httpClient: &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
		cache:      newPokemonCache(cacheTTL),
		metrics:    newMetrics(prometheus.DefaultRegisterer),
		baseURL:    getenv("POKEAPI_BASE_URL", "https://pokeapi.co/api/v2"),
	}

	r := setupRouter(s)
	port := getenv("PORT", "8080")
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}
