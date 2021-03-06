package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/daknob/eldim/config"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/julienschmidt/httprouter"
	"github.com/sirupsen/logrus"
	yaml "gopkg.in/yaml.v2"
)

var (
	conf    config.Config
	clients []config.ClientConfig
)

const (
	version = "v0.6.0"
)

func main() {

	/* Output logs in JSON or Text */
	logFormat := flag.Bool("j", false, "Output logs in JSON")

	/* Configuration File Path */
	configPath := flag.String("c", "/etc/eldim/eldim.yml", "Path to the configuration file")

	/* Parse flags */
	flag.Parse()

	/* Set the log format to JSON if requested */
	if *logFormat == true {
		logrus.SetFormatter(&logrus.JSONFormatter{})
	} else {
		logrus.SetFormatter(&logrus.TextFormatter{})
	}

	/* Startup logs */
	logrus.Printf("Starting eldim...")
	logrus.Printf("Log in JSON: %v", *logFormat)
	logrus.Printf("Configuration File: %s", *configPath)

	/* Parse the configuration file */
	logrus.Printf("Parsing the configuration file...")

	/* Open the configuration file, and read contents to RAM */
	confb, err := ioutil.ReadFile(*configPath)
	if err != nil {
		logrus.Fatalf("Could not open configuration file: %v", err)
	}

	/* Attempt to parse it for YAML */
	err = yaml.Unmarshal(confb, &conf)
	if err != nil {
		logrus.Fatalf("Could not parse the YAML configuration file: %v", err)
	}

	logrus.Printf("Configuration file loaded.")

	/* Validate configuration by appropriate function call */
	logrus.Printf("Validating parameters...")
	err = conf.Validate()
	if err != nil {
		logrus.Fatalf("Invalid configuration: %v", err)
	}
	logrus.Printf("Configuration file validated.")

	/* Load client file */
	clib, err := ioutil.ReadFile(conf.ClientFile)
	if err != nil {
		logrus.Fatalf("Could not open clients file: %v", err)
	}
	err = yaml.Unmarshal(clib, &clients)
	if err != nil {
		logrus.Fatalf("Could not parse clients YML file: %v", err)
	}

	/* Register Prometheus Metrics */
	registerPromMetrics()

	/* Update configuration-based Metrics */
	updateConfMetrics()

	/* Various web server configurations */
	logrus.Printf("Configuring the HTTP Server...")

	/* Create an HTTP Router */
	router := httprouter.New()
	router.GET("/", index)
	router.POST("/api/v1/file/upload/", v1fileUpload)

	/* Only enable Prometheus metrics if configured */
	if conf.PrometheusEnabled {
		router.GET(
			"/metrics",
			requestBasicAuth(
				conf.PrometheusAuthUser,
				conf.PrometheusAuthPass,
				"Prometheus Metrics",
				*promMetricsAuth,
				httpHandlerToHTTPRouterHandler(
					promhttp.Handler(),
				),
			),
		)
	}

	/* Configure TLS */
	tlsConfig := &tls.Config{
		PreferServerCipherSuites: true,
		CurvePreferences: []tls.CurveID{
			tls.CurveP256,
			tls.X25519,
		},
		MinVersion: tls.VersionTLS12,
	}

	/* Configure HTTP */
	server := http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       180 * time.Second,
		TLSConfig:         tlsConfig,
		Handler:           router,
		Addr:              fmt.Sprintf(":%d", conf.ListenPort),
	}

	logrus.Printf("HTTP Server Configured.")

	/* Start serving TLS */
	logrus.Printf("Serving on :%d ...", conf.ListenPort)

	err = server.ListenAndServeTLS(
		conf.TLSChainPath,
		conf.TLSKeyPath,
	)
	if err != nil {
		logrus.Fatalf("Failed to start HTTP Server: %v", err)
	}

	/* Exit */
	logrus.Printf("eldim quitting...")

}
