package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gliderlabs/ssh"
	cache "github.com/patrickmn/go-cache"
)

// mockSSHContext implements ssh.Context for testing.
type mockSSHContext struct {
	context.Context
	sync.Mutex
	user          string
	sessionID     string
	clientVersion string
	remoteAddr    net.Addr
	localAddr     net.Addr
	values        map[interface{}]interface{}
}

func newMockSSHContext(user, remoteAddr, localAddr, clientVersion string) *mockSSHContext {
	return &mockSSHContext{
		Context:       context.Background(),
		user:          user,
		clientVersion: clientVersion,
		remoteAddr:    mockAddr(remoteAddr),
		localAddr:     mockAddr(localAddr),
		values:        make(map[interface{}]interface{}),
	}
}

func (m *mockSSHContext) User() string                { return m.user }
func (m *mockSSHContext) SessionID() string            { return m.sessionID }
func (m *mockSSHContext) ClientVersion() string        { return m.clientVersion }
func (m *mockSSHContext) ServerVersion() string        { return "SSH-2.0-test" }
func (m *mockSSHContext) RemoteAddr() net.Addr         { return m.remoteAddr }
func (m *mockSSHContext) LocalAddr() net.Addr          { return m.localAddr }
func (m *mockSSHContext) Permissions() *ssh.Permissions { return nil }
func (m *mockSSHContext) SetValue(key, value interface{}) {
	m.Lock()
	defer m.Unlock()
	m.values[key] = value
}
func (m *mockSSHContext) Value(key interface{}) interface{} {
	m.Lock()
	defer m.Unlock()
	if v, ok := m.values[key]; ok {
		return v
	}
	return m.Context.Value(key)
}

type mockAddr string

func (a mockAddr) Network() string { return "tcp" }
func (a mockAddr) String() string  { return string(a) }

func TestLoadHostKey(t *testing.T) {
	validKeyDir := t.TempDir()
	validKeyPath := filepath.Join(validKeyDir, "test_key")
	_, _, err := GenerateKey(validKeyPath)
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}

	tests := []struct {
		name    string
		setup   func(t *testing.T) string
		wantErr bool
	}{
		{
			name: "valid RSA key",
			setup: func(t *testing.T) string {
				return validKeyPath
			},
			wantErr: false,
		},
		{
			name: "non-existent file",
			setup: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "nonexistent")
			},
			wantErr: true,
		},
		{
			name: "invalid key content",
			setup: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "bad_key")
				os.WriteFile(p, []byte("not a valid key"), 0600)
				return p
			},
			wantErr: true,
		},
		{
			name: "empty file",
			setup: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "empty_key")
				os.WriteFile(p, []byte{}, 0600)
				return p
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.setup(t)
			signer, err := loadHostKey(path)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				if signer != nil {
					t.Errorf("expected nil signer on error")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if signer == nil {
				t.Errorf("expected non-nil signer")
			}
		})
	}
}

func TestGetIpInfo(t *testing.T) {
	ctx, tracer := noopTracerAndCtx()

	t.Run("uses ip-api.com when no ipinfo token", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Rl", "40")
			w.Header().Set("X-Ttl", "30")
			fmt.Fprint(w, `{"status":"success","city":"Berlin","country":"DE","lat":52.52,"lon":13.405,"org":"TestOrg","timezone":"Europe/Berlin"}`)
		}))
		defer server.Close()

		origURL := ipApiBaseURL
		ipApiBaseURL = server.URL
		defer func() { ipApiBaseURL = origURL }()

		origToken := ipinfoIoToken
		ipinfoIoToken = ""
		defer func() { ipinfoIoToken = origToken }()

		c = cache.New(5*time.Minute, 10*time.Minute)

		result, err := getIpInfo("9.9.9.9", ctx, tracer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IP != "9.9.9.9" {
			t.Errorf("IP = %q, want %q", result.IP, "9.9.9.9")
		}
		if result.City != "Berlin" {
			t.Errorf("City = %q, want %q", result.City, "Berlin")
		}
		if result.Latitude != 52.52 {
			t.Errorf("Latitude = %v, want %v", result.Latitude, 52.52)
		}
	})

	t.Run("uses ipinfo.io when token is set", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"ip":"9.9.9.9","city":"Tokyo","region":"Tokyo","country":"JP","loc":"35.6762,139.6503","org":"AS1234 Test","timezone":"Asia/Tokyo"}`)
		}))
		defer server.Close()

		origURL := ipInfoIoBaseURL
		ipInfoIoBaseURL = server.URL
		defer func() { ipInfoIoBaseURL = origURL }()

		origToken := ipinfoIoToken
		ipinfoIoToken = "test-token"
		defer func() { ipinfoIoToken = origToken }()

		result, err := getIpInfo("9.9.9.9", ctx, tracer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IP != "9.9.9.9" {
			t.Errorf("IP = %q, want %q", result.IP, "9.9.9.9")
		}
		if result.City != "Tokyo" {
			t.Errorf("City = %q, want %q", result.City, "Tokyo")
		}
		if result.Latitude != 35.6762 {
			t.Errorf("Latitude = %v, want %v", result.Latitude, 35.6762)
		}
		if result.Longitude != 139.6503 {
			t.Errorf("Longitude = %v, want %v", result.Longitude, 139.6503)
		}
	})
}

func TestProcessRequest(t *testing.T) {
	_, tracer := noopTracerAndCtx()

	t.Run("skips private IP when env not set", func(t *testing.T) {
		t.Setenv("INFLUXDB_WRITE_PRIVATE_IPS", "")

		blocking := &mockWriteAPIBlocking{}
		api := InfluxdbWriteAPI{WriteAPIBlocking: blocking}

		sshCtx := newMockSSHContext("root", "192.168.1.1:54321", "0.0.0.0:2222", "SSH-2.0-test")
		sshCtx.SetValue("Function", "password")
		sshCtx.SetValue("Password", "secret")

		ctx := context.Background()
		err := processRequest(api, sshCtx, ctx, tracer)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		// Should not have written to InfluxDB
		if len(blocking.lastPoints) > 0 {
			t.Error("expected no points written for private IP")
		}
	})

	t.Run("skips loopback IP when env not set", func(t *testing.T) {
		t.Setenv("INFLUXDB_WRITE_PRIVATE_IPS", "")

		blocking := &mockWriteAPIBlocking{}
		api := InfluxdbWriteAPI{WriteAPIBlocking: blocking}

		sshCtx := newMockSSHContext("admin", "127.0.0.1:54321", "0.0.0.0:2222", "SSH-2.0-test")
		sshCtx.SetValue("Function", "password")

		ctx := context.Background()
		err := processRequest(api, sshCtx, ctx, tracer)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(blocking.lastPoints) > 0 {
			t.Error("expected no points written for loopback IP")
		}
	})

	t.Run("processes public IP and writes to InfluxDB", func(t *testing.T) {
		t.Setenv("INFLUXDB_NON_BLOCKING_WRITES", "false")

		ipServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Rl", "40")
			w.Header().Set("X-Ttl", "30")
			fmt.Fprint(w, `{"status":"success","city":"London","country":"UK","lat":51.5,"lon":-0.12,"org":"TestISP","timezone":"Europe/London"}`)
		}))
		defer ipServer.Close()

		origURL := ipApiBaseURL
		ipApiBaseURL = ipServer.URL
		defer func() { ipApiBaseURL = origURL }()

		origToken := ipinfoIoToken
		ipinfoIoToken = ""
		defer func() { ipinfoIoToken = origToken }()

		c = cache.New(5*time.Minute, 10*time.Minute)

		blocking := &mockWriteAPIBlocking{}
		api := InfluxdbWriteAPI{WriteAPIBlocking: blocking}

		sshCtx := newMockSSHContext("root", "8.8.8.8:54321", "0.0.0.0:2222", "SSH-2.0-OpenSSH")
		sshCtx.SetValue("Function", "password")
		sshCtx.SetValue("Password", "admin123")

		ctx := context.Background()
		err := processRequest(api, sshCtx, ctx, tracer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(blocking.lastPoints) == 0 {
			t.Fatal("expected points to be written for public IP")
		}
	})

	t.Run("writes private IP when INFLUXDB_WRITE_PRIVATE_IPS is true", func(t *testing.T) {
		t.Setenv("INFLUXDB_WRITE_PRIVATE_IPS", "true")
		t.Setenv("INFLUXDB_NON_BLOCKING_WRITES", "false")

		ipServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Rl", "40")
			w.Header().Set("X-Ttl", "30")
			fmt.Fprint(w, `{"status":"success","city":"Private","lat":0,"lon":0}`)
		}))
		defer ipServer.Close()

		origURL := ipApiBaseURL
		ipApiBaseURL = ipServer.URL
		defer func() { ipApiBaseURL = origURL }()

		origToken := ipinfoIoToken
		ipinfoIoToken = ""
		defer func() { ipinfoIoToken = origToken }()

		c = cache.New(5*time.Minute, 10*time.Minute)

		blocking := &mockWriteAPIBlocking{}
		api := InfluxdbWriteAPI{WriteAPIBlocking: blocking}

		sshCtx := newMockSSHContext("root", "192.168.1.1:54321", "0.0.0.0:2222", "SSH-2.0-test")
		sshCtx.SetValue("Function", "password")
		sshCtx.SetValue("Password", "pass")

		ctx := context.Background()
		err := processRequest(api, sshCtx, ctx, tracer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(blocking.lastPoints) == 0 {
			t.Fatal("expected points to be written when INFLUXDB_WRITE_PRIVATE_IPS=true")
		}
	})

	t.Run("captures context values", func(t *testing.T) {
		t.Setenv("INFLUXDB_WRITE_PRIVATE_IPS", "")

		blocking := &mockWriteAPIBlocking{}
		api := InfluxdbWriteAPI{WriteAPIBlocking: blocking}

		sshCtx := newMockSSHContext("testuser", "10.0.0.1:12345", "0.0.0.0:2222", "SSH-2.0-PuTTY")
		sshCtx.SetValue("Function", "public_key")
		sshCtx.SetValue("Key", "ssh-rsa AAAA...")

		ctx := context.Background()
		err := processRequest(api, sshCtx, ctx, tracer)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		// Private IP, so no write - but the function should still succeed
	})
}

func TestProcessRequestExponentialBackoff(t *testing.T) {
	_, tracer := noopTracerAndCtx()

	t.Run("succeeds on first try with private IP", func(t *testing.T) {
		t.Setenv("INFLUXDB_WRITE_PRIVATE_IPS", "")

		blocking := &mockWriteAPIBlocking{}
		api := InfluxdbWriteAPI{WriteAPIBlocking: blocking}

		sshCtx := newMockSSHContext("root", "192.168.1.1:54321", "0.0.0.0:2222", "SSH-2.0-test")
		sshCtx.SetValue("Function", "password")

		ctx := context.Background()
		err := processRequestExponentialBackoff(api, sshCtx, ctx, tracer)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("succeeds with public IP via httptest", func(t *testing.T) {
		t.Setenv("INFLUXDB_NON_BLOCKING_WRITES", "false")

		ipServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Rl", "40")
			w.Header().Set("X-Ttl", "30")
			fmt.Fprint(w, `{"status":"success","city":"NYC","country":"US","lat":40.71,"lon":-74.0,"org":"Test","timezone":"America/New_York"}`)
		}))
		defer ipServer.Close()

		origURL := ipApiBaseURL
		ipApiBaseURL = ipServer.URL
		defer func() { ipApiBaseURL = origURL }()

		origToken := ipinfoIoToken
		ipinfoIoToken = ""
		defer func() { ipinfoIoToken = origToken }()

		c = cache.New(5*time.Minute, 10*time.Minute)

		blocking := &mockWriteAPIBlocking{}
		api := InfluxdbWriteAPI{WriteAPIBlocking: blocking}

		sshCtx := newMockSSHContext("admin", "4.4.4.4:12345", "0.0.0.0:2222", "SSH-2.0-test")
		sshCtx.SetValue("Function", "password")
		sshCtx.SetValue("Password", "root")

		ctx := context.Background()
		err := processRequestExponentialBackoff(api, sshCtx, ctx, tracer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(blocking.lastPoints) == 0 {
			t.Fatal("expected points to be written")
		}
	})
}
