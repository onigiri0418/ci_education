package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestHealth(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := &Server{httpClient: &http.Client{}, cache: newPokemonCache(0), metrics: newMetrics(reg), baseURL: ""}
	r := setupRouter(s)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if body := w.Body.String(); body != "ok" {
		t.Fatalf("expected body 'ok', got %q", body)
	}
}

func TestPokemon(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pokemon/pikachu" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"name":"pikachu","height":4,"weight":60,"base_experience":112}`)
	}))
	defer ts.Close()

	os.Setenv("POKEAPI_BASE_URL", ts.URL)
	defer os.Unsetenv("POKEAPI_BASE_URL")

	reg := prometheus.NewRegistry()
	s := &Server{httpClient: ts.Client(), cache: newPokemonCache(0), metrics: newMetrics(reg), baseURL: ts.URL}
	r := setupRouter(s)
	req := httptest.NewRequest(http.MethodGet, "/pokemon/pikachu", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var data struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if data.Name != "pikachu" {
		t.Fatalf("expected name pikachu, got %s", data.Name)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := &Server{httpClient: &http.Client{}, cache: newPokemonCache(0), metrics: newMetrics(reg), baseURL: ""}
	r := setupRouter(s)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}
