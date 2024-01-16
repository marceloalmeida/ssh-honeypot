package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/gliderlabs/ssh"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	influxdb2api "github.com/influxdata/influxdb-client-go/v2/api"
	cache "github.com/patrickmn/go-cache"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	gossh "golang.org/x/crypto/ssh"
)

var (
	DeadlineTimeout = 30 * time.Second
	IdleTimeout     = 10 * time.Second
	ipinfoIoToken   = os.Getenv("IPINFOIO_TOKEN")
	influxdbUrl     = os.Getenv("INFLUXDB_URL")
	influxdbToken   = os.Getenv("INFLUXDB_TOKEN")
	influxdbOrg     = os.Getenv("INFLUXDB_ORG")
	influxdbBucket  = os.Getenv("INFLUXDB_BUCKET")
	hostKeyPath     = os.Getenv("HOST_KEY_PATH")
)

type IPInfo struct {
	IP        string  `json:"ip"`
	City      string  `json:"city"`
	Region    string  `json:"region"`
	Country   string  `json:"country"`
	Latitude  float64 `json:"latitute"`
	Longitude float64 `json:"longitude"`
	Org       string  `json:"org"`
	Timezone  string  `json:"timezone"`
}

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

type SSHInfo struct {
	User          string
	RemoteHost    string
	RemotePort    string
	LocalHost     string
	LocalPort     string
	ClientVersion string
	Password      string
	Key           string
	Function      string
	Timestamp     time.Time
}

type InfluxdbWriteAPI struct {
	WriteAPIBlocking influxdb2api.WriteAPIBlocking
	WriteAPI         influxdb2api.WriteAPI
}

func loadHostKey(hostKeyPath string) (ssh.Signer, error) {
	keyBytes, err := os.ReadFile(hostKeyPath)
	if err != nil {
		return nil, err
	}

	signer, err := gossh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, err
	}

	return signer, nil
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

var (
	c = cache.New(5*time.Minute, 10*time.Minute)
)

func getIpApi(host string, ctx context.Context, tracer trace.Tracer) (IpApi, error) {
	childCtx, span := tracer.Start(
		ctx,
		"getIpApi")
	defer span.End()

	log.Printf("Getting IP info for '%s' from ip-api.com", host)

	wait, found := c.Get("getIpApiRt")
	if found && wait.(time.Duration) > 0*time.Second {
		log.Printf("Rate limit key found on cache, sleeping for %s", wait)
		time.Sleep(wait.(time.Duration))
	}

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
		return IpApi{}, err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return IpApi{}, err
	}
	defer resp.Body.Close()

	respHeaderXRl, err := strconv.ParseInt(resp.Header.Get("X-Rl"), 10, 32)
	if err != nil {
		return IpApi{}, err
	}
	if resp.StatusCode == http.StatusTooManyRequests || respHeaderXRl <= 16 {
		xTtl, err := parseTime(resp.Header.Get("X-Ttl"))
		if err != nil {
			return IpApi{}, err
		}

		log.Printf("Rate limited, re-invoking request after sleeping for %s. X-Rl: %d", xTtl, respHeaderXRl)
		c.Set("getIpApiRt", xTtl, xTtl)
		time.Sleep(xTtl + 1*time.Second)
		return getIpApi(host, childCtx, tracer)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return IpApi{}, err
	}

	result, err := unmarshallgetIpApi(body, childCtx, tracer)
	if err != nil {
		return IpApi{}, err
	}

	result.IP = host

	return result, nil
}

func unmarshallgetIpApi(body []byte, ctx context.Context, tracer trace.Tracer) (IpApi, error) {
	_, span := tracer.Start(
		ctx,
		"unmarshallgetIpApi")
	defer span.End()

	var ipApi IpApi
	json.Unmarshal(body, &ipApi)
	return ipApi, nil
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
		return IPInfoIo{}, err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ipinfoIoToken))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return IPInfoIo{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return IPInfoIo{}, err
	}

	result, err := unmarshallgetIpInfoIo(body, childCtx, tracer)
	if err != nil {
		return IPInfoIo{}, err
	}

	return result, nil
}

func parseLoc(loc string, ctx context.Context, tracer trace.Tracer) (float64, float64) {
	_, span := tracer.Start(
		ctx,
		"parseLoc")
	defer span.End()

	var lat, long float64
	fmt.Sscanf(loc, "%f,%f", &lat, &long)
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
	return ipInfoIo, nil
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
		log.Printf("Writing to InfluxDB in non-blocking mode")
		errorsCh := writeAPI.WriteAPI.Errors()
		go func() {
			for err := range errorsCh {
				log.Printf("write error: %s\n", err.Error())
			}
		}()
		writeAPI.WriteAPI.WritePoint(point)
	} else {
		log.Printf("Writing to InfluxDB in blocking mode")
		err := writeAPI.WriteAPIBlocking.WritePoint(context.Background(), point)
		if err != nil {
			log.Printf("failed to write to InfluxDB: %v", err)
			return err
		}
	}

	return nil
}

func getIpInfo(host string, ctx context.Context, tracer trace.Tracer) (IPInfo, error) {
	childCtx, span := tracer.Start(
		ctx,
		"getIpInfo")
	defer span.End()

	if ipinfoIoToken != "" {
		tmp, err := getIpInfoIo(host, childCtx, tracer)
		if err != nil {
			return IPInfo{}, err
		}
		return IPInfo{
			IP:        host,
			City:      tmp.City,
			Region:    tmp.Region,
			Country:   tmp.Country,
			Latitude:  tmp.Latitude,
			Longitude: tmp.Longitude,
			Org:       tmp.Org,
			Timezone:  tmp.Timezone,
		}, nil
	} else {
		tmp, err := getIpApi(host, childCtx, tracer)
		if err != nil {
			log.Printf("Failed to get IP info from ip-api.com: %v", err)
			return IPInfo{}, err
		}

		return IPInfo{
			IP:        host,
			City:      tmp.City,
			Region:    tmp.Region,
			Country:   tmp.Country,
			Latitude:  tmp.Lat,
			Longitude: tmp.Lon,
			Org:       tmp.Org,
			Timezone:  tmp.Timezone,
		}, nil
	}
}

func processRequest(writeAPI InfluxdbWriteAPI, sshContext ssh.Context, ctx context.Context, tracer trace.Tracer) error {
	childCtx, span := tracer.Start(
		ctx,
		"processRequest")
	defer span.End()

	remote_host, remote_port, _ := net.SplitHostPort(sshContext.RemoteAddr().String())
	local_host, local_port, _ := net.SplitHostPort(sshContext.LocalAddr().String())

	sshInfo := SSHInfo{
		User:          sshContext.User(),
		RemoteHost:    remote_host,
		RemotePort:    remote_port,
		LocalHost:     local_host,
		LocalPort:     local_port,
		ClientVersion: sshContext.ClientVersion(),
	}

	sshInfo.Timestamp = time.Now()

	function := sshContext.Value("Function")
	if function != nil {
		sshInfo.Function = function.(string)
	}

	password := sshContext.Value("Password")
	if password != nil {
		sshInfo.Password = password.(string)
	}

	key := sshContext.Value("Key")
	if key != nil {
		sshInfo.Key = key.(string)
	}

	if (net.ParseIP(remote_host).IsPrivate() || net.ParseIP(remote_host).IsLoopback()) && os.Getenv("INFLUXDB_WRITE_PRIVATE_IPS") != "true" {
		log.Printf("Request to '%s' from private or loopback IP: '%s', or 'INFLUXDB_WRITE_PRIVATE_IPS' is set to '%s', skipping write to InfluxDB", sshInfo.Function, remote_host, os.Getenv("INFLUXDB_WRITE_PRIVATE_IPS"))
		sshContext.Done()
	} else {
		log.Printf("Request to '%s' from '%s'", sshInfo.Function, remote_host)
		ipInfo, err := getIpInfo(sshInfo.RemoteHost, childCtx, tracer)
		if err != nil {
			log.Printf("Failed to get IP info: %v", err)
		}

		return writeToInfluxDB(writeAPI, ipInfo, sshInfo, childCtx, tracer)
	}

	return nil
}

func main() {
	shutdown := initTracer()
	defer shutdown()

	tracer := otel.Tracer("ssh-honeypot")
	ctx := context.Background()

	if influxdbUrl == "" {
		log.Fatal("INFLUXDB_URL is not set")
	}

	if influxdbToken == "" {
		log.Fatal("INFLUXDB_TOKEN is not set")
	}

	if influxdbOrg == "" {
		log.Fatal("INFLUXDB_ORG is not set")
	}

	if influxdbBucket == "" {
		log.Fatal("INFLUXDB_BUCKET is not set")
	}

	client := influxdb2.NewClient(influxdbUrl, influxdbToken)
	defer client.Close()

	writeAPI := InfluxdbWriteAPI{
		WriteAPIBlocking: client.WriteAPIBlocking(influxdbOrg, influxdbBucket),
		WriteAPI:         client.WriteAPI(influxdbOrg, influxdbBucket),
	}
	defer writeAPI.WriteAPI.Flush()

	ssh.Handle(func(s ssh.Session) {
		s.Context().SetValue("Function", "session")

		go processRequestExponentialBackoff(writeAPI, s.Context(), ctx, tracer)

		log.Printf("Opened connection from '%s' to '%s@%s'", s.RemoteAddr().String(), s.User(), s.LocalAddr().String())

		i := 0
		for {
			i += 1
			log.Printf("Session active seconds: %d", i)
			select {
			case <-time.After(time.Second):
				continue
			case <-s.Context().Done():
				log.Printf("Closed connection from '%s' to '%s@%s'", s.RemoteAddr().String(), s.User(), s.LocalAddr().String())
				return
			}
		}
	})

	if hostKeyPath == "" {
		hostKeyPath = "./host_key"
		if _, err := os.Stat(hostKeyPath); os.IsNotExist(err) {
			log.Printf("Generating host key...")
			_, _, err := GenerateKey(hostKeyPath)
			if err != nil {
				log.Fatalf("Failed to generate host key: %v", err)
			}
		}
	}
	hostKey, err := loadHostKey(hostKeyPath)
	if err != nil {
		log.Fatalf("Failed to load host key: %v", err)
	}

	sshPort := os.Getenv("SSH_PORT")
	if sshPort == "" {
		sshPort = "2222"
	}

	log.Printf("Starting ssh server on port '%s'...", sshPort)
	log.Printf("Connections will only last %s\n", DeadlineTimeout)
	log.Printf("Timeout after %s of no activity\n", IdleTimeout)
	server := &ssh.Server{
		Addr:        ":" + sshPort,
		MaxTimeout:  DeadlineTimeout,
		IdleTimeout: IdleTimeout,
		Version:     "OpenSSH_7.4p1 Debian-10+deb9u7",
		PublicKeyHandler: func(s ssh.Context, key ssh.PublicKey) bool {
			s.SetValue("Function", "public_key")
			s.SetValue("Key", string(gossh.MarshalAuthorizedKey(key)))
			go processRequestExponentialBackoff(writeAPI, s, ctx, tracer)
			return false
		},
		PasswordHandler: func(s ssh.Context, password string) bool {
			s.SetValue("Function", "password")
			s.SetValue("Password", password)
			go processRequestExponentialBackoff(writeAPI, s, ctx, tracer)

			return false
		},
	}

	server.AddHostKey(hostKey)
	log.Fatal(server.ListenAndServe())
}

func processRequestExponentialBackoff(writeAPI InfluxdbWriteAPI, sshContext ssh.Context, ctx context.Context, tracer trace.Tracer) error {
	childCtx, span := tracer.Start(
		ctx,
		"processRequestExponentialBackoff")
	defer span.End()

	backoffSettings := backoff.NewExponentialBackOff()
	backoffSettings.MaxElapsedTime = 1 * time.Minute

	operation := func() error {
		return processRequest(writeAPI, sshContext, childCtx, tracer)
	}

	err := backoff.Retry(operation, backoffSettings)
	if err != nil {
		log.Printf("Failed to process request: %v", err)
		return err
	}

	log.Printf("Successfully processed request")
	return nil
}
