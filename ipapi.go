package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	cache "github.com/patrickmn/go-cache"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type IpApi struct {
	IP            string  `json:"ip"`
	Status        string  `json:"status"`
	Continent     string  `json:"continent"`
	ContinentCode string  `json:"continentCode"`
	Country       string  `json:"country"`
	CountryCode   string  `json:"countryCode"`
	Region        string  `json:"region"`
	RegionName    string  `json:"regionName"`
	City          string  `json:"city"`
	District      string  `json:"district"`
	Zip           string  `json:"zip"`
	Lat           float64 `json:"lat"`
	Lon           float64 `json:"lon"`
	Timezone      string  `json:"timezone"`
	Offset        string  `json:"offset"`
	Currency      string  `json:"currency"`
	Isp           string  `json:"isp"`
	Org           string  `json:"org"`
	As            string  `json:"as"`
	Asname        string  `json:"asname"`
	Reverse       string  `json:"reverse"`
	Mobile        string  `json:"mobile"`
	Proxy         string  `json:"proxy"`
	Hosting       string  `json:"hosting"`
	Query         string  `json:"query"`
}

var (
	c = cache.New(5*time.Minute, 10*time.Minute)
)

func getIpApi(host string, ctx context.Context, tracer trace.Tracer) (IpApi, error) {
	childCtx, span := tracer.Start(
		ctx,
		"getIpApi")
	defer span.End()

	wait, found := c.Get("getIpApiRt")
	if found && wait.(time.Duration) > 0*time.Second {
		span.AddEvent("Rate limit key found on cache, sleeping")
		log.Printf("Rate limit key found on cache, sleeping for %s", wait)
		time.Sleep(wait.(time.Duration))
	}

	span.AddEvent("Getting IP info from ip-api.com")
	log.Printf("Getting IP info for '%s' from ip-api.com", host)

	fields := []string{
		"status",
		"message",
		"continent",
		"continentCode",
		"country",
		"countryCode",
		"region",
		"regionName",
		"city",
		"district",
		"zip",
		"lat",
		"lon",
		"timezone",
		"offset",
		"currency",
		"isp",
		"org",
		"as",
		"asname",
		"reverse",
		"mobile",
		"proxy",
		"hosting",
		"query",
	}

	url := fmt.Sprintf("http://ip-api.com/json/%s?fields=%s", host, strings.Join(fields, ","))
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		span.AddEvent("Error creating request for ip-api.com, re-invoking request after sleeping")
		if found {
			log.Printf("Error creating request for ip-api.com, re-invoking request after sleeping for %s", wait)
			c.Set("getIpApiRt", wait.(time.Duration)+1*time.Second, wait.(time.Duration)+1*time.Second)
		} else {
			log.Printf("Error creating request for ip-api.com, re-invoking request after sleeping for 1 second")
			c.Set("getIpApiRt", 1*time.Second, 1*time.Second)
		}

		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return IpApi{}, err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return IpApi{}, err
	}
	defer resp.Body.Close()

	respHeaderXRl, err := strconv.ParseInt(resp.Header.Get("X-Rl"), 10, 32)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return IpApi{}, err
	}

	if resp.StatusCode == http.StatusTooManyRequests || respHeaderXRl <= 16 {
		xTtl, err := parseTime(resp.Header.Get("X-Ttl"))
		xTtl += time.Duration(1+rand.Int63n(respHeaderXRl+1)) * time.Second
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return IpApi{}, err
		}

		span.AddEvent("Rate limited, re-invoking request after sleeping")
		span.SetStatus(codes.Error, fmt.Sprintf("Rate limited, re-invoking request after sleeping for %s. X-Rl: %d", xTtl, respHeaderXRl))
		log.Printf("Rate limited, re-invoking request after sleeping for %s. X-Rl: %d", xTtl, respHeaderXRl)

		c.Set("getIpApiRt", xTtl, xTtl)

		return IpApi{}, fmt.Errorf("rate limited")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return IpApi{}, err
	}

	result, err := unmarshallgetIpApi(body, childCtx, tracer)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return IpApi{}, err
	}

	result.IP = host

	span.AddEvent("Successfully got IP info from ip-api.com")
	span.SetStatus(codes.Ok, fmt.Sprintf("Successfully got IP info for '%s' from ip-api.com", host))
	return result, nil
}

func unmarshallgetIpApi(body []byte, ctx context.Context, tracer trace.Tracer) (IpApi, error) {
	_, span := tracer.Start(
		ctx,
		"unmarshallgetIpApi")
	defer span.End()

	var ipApi IpApi
	json.Unmarshal(body, &ipApi)

	span.AddEvent("Successfully unmarshalled IP info from ip-api.com")
	span.SetStatus(codes.Ok, "Successfully unmarshalled IP info from ip-api.com")
	return ipApi, nil
}

func parseTime(headerValue string) (time.Duration, error) {
	seconds, err := time.ParseDuration(headerValue + "s")
	if err == nil {
		return seconds, nil
	}

	date, err := time.Parse(time.RFC1123, headerValue)
	if err != nil {
		return 0, fmt.Errorf("unable to parse Time header: %v", err)
	}

	return time.Until(date), nil
}
