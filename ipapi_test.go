package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cache "github.com/patrickmn/go-cache"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func noopTracerAndCtx() (context.Context, trace.Tracer) {
	return context.Background(), noop.NewTracerProvider().Tracer("test")
}

func TestParseTime(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantErr   bool
		checkDur  func(time.Duration) bool
		checkDesc string
	}{
		{
			name:      "numeric seconds",
			input:     "45",
			wantErr:   false,
			checkDur:  func(d time.Duration) bool { return d == 45*time.Second },
			checkDesc: "expected 45s",
		},
		{
			name:      "zero seconds",
			input:     "0",
			wantErr:   false,
			checkDur:  func(d time.Duration) bool { return d == 0 },
			checkDesc: "expected 0s",
		},
		{
			name:      "large numeric seconds",
			input:     "3600",
			wantErr:   false,
			checkDur:  func(d time.Duration) bool { return d == 3600*time.Second },
			checkDesc: "expected 3600s",
		},
		{
			name:    "RFC1123 future date",
			input:   time.Now().Add(60 * time.Second).UTC().Format(time.RFC1123),
			wantErr: false,
			checkDur: func(d time.Duration) bool {
				return d > 50*time.Second && d < 70*time.Second
			},
			checkDesc: "expected ~60s",
		},
		{
			name:    "RFC1123 past date",
			input:   time.Now().Add(-60 * time.Second).UTC().Format(time.RFC1123),
			wantErr: false,
			checkDur: func(d time.Duration) bool {
				return d < 0
			},
			checkDesc: "expected negative duration",
		},
		{
			name:    "garbage string",
			input:   "not-a-time",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dur, err := parseTime(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if tt.checkDur != nil && !tt.checkDur(dur) {
				t.Errorf("duration check failed (%s): got %v", tt.checkDesc, dur)
			}
		})
	}
}

func TestUnmarshallgetIpApi(t *testing.T) {
	ctx, tracer := noopTracerAndCtx()

	tests := []struct {
		name       string
		body       string
		wantStatus string
		wantCity   string
		wantLat    float64
		wantLon    float64
	}{
		{
			name:       "valid full JSON",
			body:       `{"status":"success","city":"London","country":"UK","lat":51.5,"lon":-0.12,"org":"TestISP","timezone":"Europe/London"}`,
			wantStatus: "success",
			wantCity:   "London",
			wantLat:    51.5,
			wantLon:    -0.12,
		},
		{
			name:       "minimal JSON",
			body:       `{"status":"success","lat":1.5,"lon":2.5}`,
			wantStatus: "success",
			wantLat:    1.5,
			wantLon:    2.5,
		},
		{
			name: "empty JSON object",
			body: `{}`,
		},
		{
			name: "invalid JSON",
			body: `not json`,
		},
		{
			name: "empty body",
			body: ``,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unmarshallgetIpApi([]byte(tt.body), ctx, tracer)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if result.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", result.Status, tt.wantStatus)
			}
			if result.City != tt.wantCity {
				t.Errorf("City = %q, want %q", result.City, tt.wantCity)
			}
			if result.Lat != tt.wantLat {
				t.Errorf("Lat = %v, want %v", result.Lat, tt.wantLat)
			}
			if result.Lon != tt.wantLon {
				t.Errorf("Lon = %v, want %v", result.Lon, tt.wantLon)
			}
		})
	}
}

func TestGetIpApi(t *testing.T) {
	ctx, tracer := noopTracerAndCtx()

	t.Run("successful response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Rl", "40")
			w.Header().Set("X-Ttl", "30")
			fmt.Fprint(w, `{"status":"success","city":"London","country":"UK","lat":51.5,"lon":-0.12,"org":"TestISP","timezone":"Europe/London"}`)
		}))
		defer server.Close()

		origURL := ipApiBaseURL
		ipApiBaseURL = server.URL
		defer func() { ipApiBaseURL = origURL }()

		// Clear rate limit cache
		c = cache.New(5*time.Minute, 10*time.Minute)

		result, err := getIpApi("1.2.3.4", ctx, tracer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IP != "1.2.3.4" {
			t.Errorf("IP = %q, want %q", result.IP, "1.2.3.4")
		}
		if result.City != "London" {
			t.Errorf("City = %q, want %q", result.City, "London")
		}
		if result.Lat != 51.5 {
			t.Errorf("Lat = %v, want %v", result.Lat, 51.5)
		}
	})

	t.Run("rate limited response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Rl", "0")
			w.Header().Set("X-Ttl", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"status":"fail","message":"rate limited"}`)
		}))
		defer server.Close()

		origURL := ipApiBaseURL
		ipApiBaseURL = server.URL
		defer func() { ipApiBaseURL = origURL }()

		c = cache.New(5*time.Minute, 10*time.Minute)

		_, err := getIpApi("1.2.3.4", ctx, tracer)
		if err == nil {
			t.Fatal("expected error for rate-limited response, got nil")
		}
	})

	t.Run("low remaining requests triggers rate limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Rl", "10")
			w.Header().Set("X-Ttl", "5")
			fmt.Fprint(w, `{"status":"success","city":"Paris","lat":48.85,"lon":2.35}`)
		}))
		defer server.Close()

		origURL := ipApiBaseURL
		ipApiBaseURL = server.URL
		defer func() { ipApiBaseURL = origURL }()

		c = cache.New(5*time.Minute, 10*time.Minute)

		_, err := getIpApi("5.6.7.8", ctx, tracer)
		if err == nil {
			t.Fatal("expected rate limit error when X-Rl <= 16, got nil")
		}
	})

	t.Run("server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Rl", "40")
			w.Header().Set("X-Ttl", "30")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"status":"fail"}`)
		}))
		defer server.Close()

		origURL := ipApiBaseURL
		ipApiBaseURL = server.URL
		defer func() { ipApiBaseURL = origURL }()

		c = cache.New(5*time.Minute, 10*time.Minute)

		result, err := getIpApi("1.2.3.4", ctx, tracer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "fail" {
			t.Errorf("Status = %q, want %q", result.Status, "fail")
		}
	})

	t.Run("missing X-Rl header", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"status":"success"}`)
		}))
		defer server.Close()

		origURL := ipApiBaseURL
		ipApiBaseURL = server.URL
		defer func() { ipApiBaseURL = origURL }()

		c = cache.New(5*time.Minute, 10*time.Minute)

		_, err := getIpApi("1.2.3.4", ctx, tracer)
		if err == nil {
			t.Fatal("expected error when X-Rl header is missing")
		}
	})
}
