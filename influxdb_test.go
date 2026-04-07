package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	influxdb2api "github.com/influxdata/influxdb-client-go/v2/api"
	"github.com/influxdata/influxdb-client-go/v2/api/write"
)

type mockWriteAPIBlocking struct {
	lastPoints []*write.Point
	err        error
}

func (m *mockWriteAPIBlocking) WriteRecord(ctx context.Context, line ...string) error {
	return m.err
}

func (m *mockWriteAPIBlocking) WritePoint(ctx context.Context, point ...*write.Point) error {
	m.lastPoints = point
	return m.err
}

func (m *mockWriteAPIBlocking) EnableBatching() {}

func (m *mockWriteAPIBlocking) Flush(ctx context.Context) error {
	return m.err
}

type mockWriteAPI struct {
	lastPoint *write.Point
	errorsCh  chan error
}

func (m *mockWriteAPI) WriteRecord(line string) {}

func (m *mockWriteAPI) WritePoint(point *write.Point) {
	m.lastPoint = point
}

func (m *mockWriteAPI) Flush() {}

func (m *mockWriteAPI) Errors() <-chan error {
	return m.errorsCh
}

func (m *mockWriteAPI) SetWriteFailedCallback(cb influxdb2api.WriteFailedCallback) {}

func testIPInfo() IPInfo {
	return IPInfo{
		IP: "1.2.3.4", City: "TestCity", Region: "TestRegion",
		Country: "TC", Latitude: 10.0, Longitude: 20.0,
		Org: "TestOrg", Timezone: "UTC",
	}
}

func testSSHInfo() SSHInfo {
	return SSHInfo{
		User: "root", RemoteHost: "1.2.3.4", RemotePort: "54321",
		LocalHost: "0.0.0.0", LocalPort: "2222",
		ClientVersion: "SSH-2.0-test", Password: "pass123",
		Function: "password", Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestWriteToInfluxDB(t *testing.T) {
	ctx, tracer := noopTracerAndCtx()

	t.Run("blocking write success", func(t *testing.T) {
		t.Setenv("INFLUXDB_NON_BLOCKING_WRITES", "false")
		blocking := &mockWriteAPIBlocking{}
		api := InfluxdbWriteAPI{WriteAPIBlocking: blocking}

		err := writeToInfluxDB(api, testIPInfo(), testSSHInfo(), ctx, tracer)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("blocking write failure", func(t *testing.T) {
		t.Setenv("INFLUXDB_NON_BLOCKING_WRITES", "false")
		blocking := &mockWriteAPIBlocking{err: fmt.Errorf("db down")}
		api := InfluxdbWriteAPI{WriteAPIBlocking: blocking}

		err := writeToInfluxDB(api, testIPInfo(), testSSHInfo(), ctx, tracer)
		if err == nil {
			t.Errorf("expected error, got nil")
		}
	})

	t.Run("non-blocking write success", func(t *testing.T) {
		t.Setenv("INFLUXDB_NON_BLOCKING_WRITES", "true")
		nonBlocking := &mockWriteAPI{errorsCh: make(chan error, 1)}
		api := InfluxdbWriteAPI{WriteAPI: nonBlocking}

		err := writeToInfluxDB(api, testIPInfo(), testSSHInfo(), ctx, tracer)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if nonBlocking.lastPoint == nil {
			t.Errorf("expected WritePoint to be called")
		}
	})

	t.Run("blocking write captures correct point data", func(t *testing.T) {
		t.Setenv("INFLUXDB_NON_BLOCKING_WRITES", "false")
		blocking := &mockWriteAPIBlocking{}
		api := InfluxdbWriteAPI{WriteAPIBlocking: blocking}

		ipInfo := testIPInfo()
		sshInfo := testSSHInfo()
		err := writeToInfluxDB(api, ipInfo, sshInfo, ctx, tracer)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}
		if len(blocking.lastPoints) == 0 {
			t.Fatal("no points written")
		}
	})
}
