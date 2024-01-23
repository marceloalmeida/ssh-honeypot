package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/gliderlabs/ssh"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
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

func getIpInfo(host string, ctx context.Context, tracer trace.Tracer) (IPInfo, error) {
	childCtx, span := tracer.Start(
		ctx,
		"getIpInfo")
	defer span.End()

	if ipinfoIoToken != "" {
		tmp, err := getIpInfoIo(host, childCtx, tracer)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return IPInfo{}, err
		}

		span.AddEvent("Got IP info from ipinfo.io")
		span.SetStatus(codes.Ok, fmt.Sprintf("Got IP info from ipinfo.io for '%s'", host))

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
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return IPInfo{}, err
		}

		span.AddEvent("Got IP info from ip-api.com'")
		span.SetStatus(codes.Ok, fmt.Sprintf("Got IP info from ip-api.com for '%s'", host))

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
		span.AddEvent("Request from private or loopback IP, or 'INFLUXDB_WRITE_PRIVATE_IPS' is set, skipping write to InfluxDB")
		log.Printf("Request to '%s' from private or loopback IP: '%s', or 'INFLUXDB_WRITE_PRIVATE_IPS' is set to '%s', skipping write to InfluxDB", sshInfo.Function, remote_host, os.Getenv("INFLUXDB_WRITE_PRIVATE_IPS"))
		sshContext.Done()
	} else {
		span.AddEvent("Request inccoming")
		log.Printf("Request to '%s' from '%s'", sshInfo.Function, remote_host)
		ipInfo, err := getIpInfo(sshInfo.RemoteHost, childCtx, tracer)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			log.Printf("Failed to get IP info: %v", err)
			return err
		}

		if writeToInfluxDB(writeAPI, ipInfo, sshInfo, childCtx, tracer) != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			log.Printf("Failed to write to InfluxDB: %v", err)
			return err
		}
	}

	span.AddEvent("Request successfully processed")
	span.SetStatus(codes.Ok, fmt.Sprintf("Request to '%s' from '%s' successfully processed", sshInfo.Function, remote_host))
	return nil
}

func processRequestExponentialBackoff(writeAPI InfluxdbWriteAPI, sshContext ssh.Context, ctx context.Context, tracer trace.Tracer) error {
	childCtx, span := tracer.Start(
		ctx,
		"processRequestExponentialBackoff")
	defer span.End()

	backoffSettings := backoff.NewExponentialBackOff()
	backoffSettings.MaxElapsedTime = 30 * time.Minute
	backoffContext := backoff.WithContext(backoffSettings, childCtx)

	operation := func() error {
		return processRequest(writeAPI, sshContext, backoffContext.Context(), tracer)
	}

	err := backoff.Retry(operation, backoffContext)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		log.Printf("Failed to process request: %v", err)
		return err
	}

	span.AddEvent("Successfully processed request")
	span.SetStatus(codes.Ok, "Successfully processed request")
	log.Printf("Successfully processed request")
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
