package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type IPInfoIo struct {
	IP        string  `json:"ip"`
	Hostname  string  `json:"hostname"`
	City      string  `json:"city"`
	Region    string  `json:"region"`
	Country   string  `json:"country"`
	Loc       string  `json:"loc"`
	Org       string  `json:"org"`
	Postal    string  `json:"postal"`
	Timezone  string  `json:"timezone"`
	Latitude  float64 `json:"latitute"`
	Longitude float64 `json:"longitude"`
}

func getIpInfoIo(host string, ctx context.Context, tracer trace.Tracer) (IPInfoIo, error) {
	childCtx, span := tracer.Start(
		ctx,
		"getIpInfoIo")
	defer span.End()

	log.Printf("Getting IP info for '%s' from ipinfo.io", host)
	url := fmt.Sprintf("https://ipinfo.io/%s", host)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return IPInfoIo{}, err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ipinfoIoToken))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return IPInfoIo{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return IPInfoIo{}, err
	}

	result, err := unmarshallgetIpInfoIo(body, childCtx, tracer)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return IPInfoIo{}, err
	}

	span.AddEvent("Successfully got IP info from ipinfo.io")
	span.SetStatus(codes.Ok, fmt.Sprintf("Successfully got IP info for '%s' from ipinfo.io", host))

	return result, nil
}

func parseLoc(loc string, ctx context.Context, tracer trace.Tracer) (float64, float64) {
	_, span := tracer.Start(
		ctx,
		"parseLoc")
	defer span.End()

	var lat, long float64
	fmt.Sscanf(loc, "%f,%f", &lat, &long)

	span.AddEvent("Successfully parsed location")
	span.SetStatus(codes.Ok, "Successfully parsed location")

	return lat, long
}

func unmarshallgetIpInfoIo(body []byte, ctx context.Context, tracer trace.Tracer) (IPInfoIo, error) {
	childCtx, span := tracer.Start(
		ctx,
		"unmarshallgetIpInfoIo")
	defer span.End()

	var ipInfoIo IPInfoIo
	json.Unmarshal(body, &ipInfoIo)
	lat, long := parseLoc(ipInfoIo.Loc, childCtx, tracer)
	ipInfoIo.Latitude = lat
	ipInfoIo.Longitude = long

	span.AddEvent("Successfully unmarshalled IP info from ipinfo.io")
	span.SetStatus(codes.Ok, "Successfully unmarshalled IP info from ipinfo.io")

	return ipInfoIo, nil
}
