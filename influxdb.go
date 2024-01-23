package main

import (
	"context"
	"log"
	"os"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	influxdb2api "github.com/influxdata/influxdb-client-go/v2/api"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type InfluxdbWriteAPI struct {
	WriteAPIBlocking influxdb2api.WriteAPIBlocking
	WriteAPI         influxdb2api.WriteAPI
}

func writeToInfluxDB(writeAPI InfluxdbWriteAPI, ipInfo IPInfo, sshInfo SSHInfo, ctx context.Context, tracer trace.Tracer) error {
	_, span := tracer.Start(
		ctx,
		"writeToInfluxDB")
	defer span.End()

	point := influxdb2.NewPointWithMeasurement("request").
		AddField("latitude", ipInfo.Latitude).
		AddField("longitude", ipInfo.Longitude).
		AddTag("ip", ipInfo.IP).
		AddTag("country", ipInfo.Country).
		AddTag("city", ipInfo.City).
		AddTag("region", ipInfo.Region).
		AddTag("org", ipInfo.Org).
		AddTag("timezone", ipInfo.Timezone).
		AddTag("user", sshInfo.User).
		AddTag("remote_host", sshInfo.RemoteHost).
		AddTag("remote_port", sshInfo.RemotePort).
		AddTag("local_host", sshInfo.LocalHost).
		AddTag("local_port", sshInfo.LocalPort).
		AddTag("client_version", sshInfo.ClientVersion).
		AddTag("function", sshInfo.Function).
		AddTag("password", sshInfo.Password).
		AddTag("key", sshInfo.Key).
		SetTime(sshInfo.Timestamp)

	if os.Getenv("INFLUXDB_NON_BLOCKING_WRITES") == "true" {
		span.AddEvent("Writing to InfluxDB in non-blocking mode")
		log.Printf("Writing to InfluxDB in non-blocking mode")
		errorsCh := writeAPI.WriteAPI.Errors()
		go func() error {
			for err := range errorsCh {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				log.Printf("write error: %s\n", err.Error())
				return err
			}

			return nil
		}()
		writeAPI.WriteAPI.WritePoint(point)
	} else {
		span.AddEvent("Writing to InfluxDB in blocking mode")
		log.Printf("Writing to InfluxDB in blocking mode")
		err := writeAPI.WriteAPIBlocking.WritePoint(context.Background(), point)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			log.Printf("failed to write to InfluxDB: %v", err)
			return err
		}
	}

	span.AddEvent("Successfully wrote to InfluxDB")
	span.SetStatus(codes.Ok, "Successfully wrote to InfluxDB")
	return nil
}
