package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseLoc(t *testing.T) {
	ctx, tracer := noopTracerAndCtx()

	tests := []struct {
		name    string
		loc     string
		wantLat float64
		wantLng float64
	}{
		{
			name:    "normal coords",
			loc:     "37.7749,-122.4194",
			wantLat: 37.7749,
			wantLng: -122.4194,
		},
		{
			name:    "zero coords",
			loc:     "0,0",
			wantLat: 0,
			wantLng: 0,
		},
		{
			name:    "negative coords",
			loc:     "-33.8688,151.2093",
			wantLat: -33.8688,
			wantLng: 151.2093,
		},
		{
			name:    "empty string",
			loc:     "",
			wantLat: 0,
			wantLng: 0,
		},
		{
			name:    "single value",
			loc:     "37.7749",
			wantLat: 37.7749,
			wantLng: 0,
		},
		{
			name:    "non-numeric",
			loc:     "abc,def",
			wantLat: 0,
			wantLng: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lat, lng := parseLoc(tt.loc, ctx, tracer)
			if lat != tt.wantLat {
				t.Errorf("lat = %v, want %v", lat, tt.wantLat)
			}
			if lng != tt.wantLng {
				t.Errorf("lng = %v, want %v", lng, tt.wantLng)
			}
		})
	}
}

func TestUnmarshallgetIpInfoIo(t *testing.T) {
	ctx, tracer := noopTracerAndCtx()

	tests := []struct {
		name    string
		body    string
		wantIP  string
		wantLat float64
		wantLng float64
		wantCty string
	}{
		{
			name:    "valid JSON with loc",
			body:    `{"ip":"1.2.3.4","city":"San Francisco","region":"California","country":"US","loc":"37.7749,-122.4194","org":"AS1234 Test","timezone":"America/Los_Angeles"}`,
			wantIP:  "1.2.3.4",
			wantCty: "San Francisco",
			wantLat: 37.7749,
			wantLng: -122.4194,
		},
		{
			name:    "valid JSON empty loc",
			body:    `{"ip":"1.2.3.4","city":"Test","loc":""}`,
			wantIP:  "1.2.3.4",
			wantCty: "Test",
			wantLat: 0,
			wantLng: 0,
		},
		{
			name: "empty JSON",
			body: `{}`,
		},
		{
			name: "invalid JSON",
			body: `garbage`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := unmarshallgetIpInfoIo([]byte(tt.body), ctx, tracer)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if result.IP != tt.wantIP {
				t.Errorf("IP = %q, want %q", result.IP, tt.wantIP)
			}
			if result.City != tt.wantCty {
				t.Errorf("City = %q, want %q", result.City, tt.wantCty)
			}
			if result.Latitude != tt.wantLat {
				t.Errorf("Latitude = %v, want %v", result.Latitude, tt.wantLat)
			}
			if result.Longitude != tt.wantLng {
				t.Errorf("Longitude = %v, want %v", result.Longitude, tt.wantLng)
			}
		})
	}
}

func TestGetIpInfoIo(t *testing.T) {
	ctx, tracer := noopTracerAndCtx()

	t.Run("successful response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "" {
				t.Error("expected Authorization header to be set")
			}
			fmt.Fprint(w, `{"ip":"8.8.8.8","city":"Mountain View","region":"California","country":"US","loc":"37.386,-122.0838","org":"AS15169 Google LLC","timezone":"America/Los_Angeles"}`)
		}))
		defer server.Close()

		origURL := ipInfoIoBaseURL
		ipInfoIoBaseURL = server.URL
		defer func() { ipInfoIoBaseURL = origURL }()

		result, err := getIpInfoIo("8.8.8.8", ctx, tracer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.City != "Mountain View" {
			t.Errorf("City = %q, want %q", result.City, "Mountain View")
		}
		if result.Latitude != 37.386 {
			t.Errorf("Latitude = %v, want %v", result.Latitude, 37.386)
		}
		if result.Longitude != -122.0838 {
			t.Errorf("Longitude = %v, want %v", result.Longitude, -122.0838)
		}
	})

	t.Run("server returns error status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error":"forbidden"}`)
		}))
		defer server.Close()

		origURL := ipInfoIoBaseURL
		ipInfoIoBaseURL = server.URL
		defer func() { ipInfoIoBaseURL = origURL }()

		// Even with non-200 status, the function reads the body and unmarshals
		result, err := getIpInfoIo("8.8.8.8", ctx, tracer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// The IP field won't be set since the error response doesn't have it
		if result.IP != "" {
			t.Errorf("IP = %q, want empty", result.IP)
		}
	})

	t.Run("authorization header contains token", func(t *testing.T) {
		var gotAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			fmt.Fprint(w, `{"ip":"1.1.1.1","loc":"0,0"}`)
		}))
		defer server.Close()

		origURL := ipInfoIoBaseURL
		ipInfoIoBaseURL = server.URL
		defer func() { ipInfoIoBaseURL = origURL }()

		origToken := ipinfoIoToken
		ipinfoIoToken = "test-token-123"
		defer func() { ipinfoIoToken = origToken }()

		_, err := getIpInfoIo("1.1.1.1", ctx, tracer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotAuth != "Bearer test-token-123" {
			t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-token-123")
		}
	})

	t.Run("connection refused", func(t *testing.T) {
		origURL := ipInfoIoBaseURL
		ipInfoIoBaseURL = "http://127.0.0.1:1"
		defer func() { ipInfoIoBaseURL = origURL }()

		_, err := getIpInfoIo("1.2.3.4", ctx, tracer)
		if err == nil {
			t.Fatal("expected error for connection refused, got nil")
		}
	})
}
