// Copyright © 2018 Joel Baranick <jbaranick@gmail.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// Based on:
// http://github.com/marcopaganini/smartcollector
// (C) 2016 by Marco Paganini <paganini@paganini.net>

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/kadaan/gosmart"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	plog "github.com/prometheus/common/log"
	"github.com/prometheus/common/version"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"gopkg.in/alecthomas/kingpin.v2"
)

const (
	namespace = "smartthings"
)

var (
	application = kingpin.New("smartthings_exporter", "Smartthings exporter for Prometheus")

	registerCommand        *kingpin.CmdClause
	registerPort           *uint16
	registerOAuthClient    *string
	registerOAuthSecret    *string
	registerOAuthTokenFile **os.File

	monitorCommand        *kingpin.CmdClause
	listenAddress         *string
	metricsPath           *string
	monitorOAuthClient    *string
	monitorOAuthTokenFile *string

	valOpenClosed     = []string{"open", "closed"}
	valInactiveActive = []string{"inactive", "active"}
	valAbsentPresent  = []string{"not present", "present"}
	valOffOn          = []string{"off", "on"}
	invalidMetric     = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "smartthings_invalid_metric",
			Help: "Total number of metrics that were invalid.",
		},
	)
	unknownMetric = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "smartthings_unknown_metric",
			Help: "Total number of metrics that exporter didn't know.",
		},
	)
	metrics = map[string]*metric{
		"alarmState": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "alarm_cleared"), "0 if the alarm is clear.",
			[]string{"id", "name"}, nil), valueClear},

		"battery": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "battery_percentage"),
			"Percentage of battery remaining.", []string{"id", "name"}, nil), valueFloat},

		"carbonMonoxide": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "contact_closed"),
			"1 if the contact is closed.", []string{"id", "name"}, nil), valueClear},

		"contact": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "contact_closed"),
			"1 if the contact is closed.", []string{"id", "name"}, nil),
			func(i interface{}) (f float64, e error) {
				return valueOneOf(i, valOpenClosed)
			}},

		"energy": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "energy_usage_joules"),
			"Energy usage in joules.", []string{"id", "name"}, nil),
			func(i interface{}) (f float64, e error) {
				value, err := valueFloat(i)
				if err != nil {
					return 0, err
				}
				return value * 3600000, err
			}},

		"motion": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "motion_detected"),
			"1 if presence is detected.", []string{"id", "name"}, nil),
			func(i interface{}) (f float64, e error) {
				return valueOneOf(i, valInactiveActive)
			}},

		"power": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "power_usage_watts"),
			"Current power usage in watts.", []string{"id", "name"}, nil), valueFloat},

		"presence": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "presence_detected"),
			"1 if presence is detected.", []string{"id", "name"}, nil),
			func(i interface{}) (f float64, e error) {
				return valueOneOf(i, valAbsentPresent)
			}},

		"smoke": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "smoke_detected"), "1 if smoke is detected.",
			[]string{"id", "name"}, nil), valueClear},

		"switch": {prometheus.NewDesc(prometheus.BuildFQName(namespace, "", "switch_enabled"),
			"1 if the switch is on.", []string{"id", "name"}, nil),
			func(i interface{}) (f float64, e error) {
				return valueOneOf(i, valOffOn)
			}},

		"temperature": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "temperature_fahrenheit"),
			"Temperature in fahrenheit.", []string{"id", "name"}, nil), valueFloat},
	}
)

type metric struct {
	description *prometheus.Desc
	valueMapper func(interface{}) (float64, error)
}

// Exporter collects smartthings stats and exports them using the prometheus metrics package.
type Exporter struct {
	client   *http.Client
	endpoint string
}

// NewExporter returns an initialized Exporter.
func NewExporter(oauthClient string, oauthToken *oauth2.Token) (*Exporter, error) {
	// Create the oauth2.config object with no secret to use with the token we already have
	config := gosmart.NewOAuthConfig(oauthClient, "")

	// Create a client with the token and fetch endpoints URI.
	ctx := context.Background()
	client := config.Client(ctx, oauthToken)
	endpoint, err := gosmart.GetEndPointsURI(client)
	if err != nil {
		plog.Fatalf("Error reading endpoints URI: %v\n", err)
	}

	_, verr := gosmart.GetDevices(client, endpoint)
	if verr != nil {
		plog.Fatalf("Error verifying connection to endpoints URI %v: %v\n", endpoint, err)
	}

	// Init our exporter.
	return &Exporter{
		client:   client,
		endpoint: endpoint,
	}, nil
}

// Describe describes all the metrics ever exported by the Kafka exporter. It
// implements prometheus.Collector.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range metrics {
		ch <- m.description
	}
}

// Collect fetches the stats from configured Kafka location and delivers them
// as Prometheus metrics. It implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	// Iterate over all devices and collect timeseries info.
	devs, err := gosmart.GetDevices(e.client, e.endpoint)
	if err != nil {
		plog.Errorf("Error reading list of devices from %v: %v\n", e.endpoint, err)
	}

	// TODO run in parallel to reduce time
	for _, dev := range devs {
		devinfo, errdi := gosmart.GetDeviceInfo(e.client, e.endpoint, dev.ID)
		if errdi != nil {
			plog.Errorf("GetDeviceInfo Failed => %v", err)
			continue
		}

		plog.Debugf("Dev> %s Id:%s - Fetching Attributes => %d\n", dev.DisplayName, dev.ID, len(devinfo.Attributes))

		for k, val := range devinfo.Attributes {
			if val == nil {
				val = ""
			}

			var value float64
			//var metricDesc *prometheus.Desc
			metric := metrics[k]
			if metric == nil {
				unknownMetric.Inc()
				plog.Debugf("  Attr> '%s' [val=%v] - Unknown", k, val)
				continue
			}
			value, err = metric.valueMapper(val)
			plog.Debugf("  Attr> '%s' [val=%f] - %s", k, value, metric.description)

			if err == nil {
				ch <- prometheus.MustNewConstMetric(metric.description, prometheus.GaugeValue, value, dev.ID, dev.DisplayName)
			} else {
				invalidMetric.Inc()
				plog.Errorf("%s - '%s' [val=%f] - %v", dev.DisplayName, k, value, err)
			}
		}
	}
}

// valueClear expects a string and returns 0 for "clear", 1 for anything else.
// TODO: Expand this to properly identify non-clear conditions and return error
// in case an unexpected value is found.
func valueClear(v interface{}) (float64, error) {
	val, ok := v.(string)
	if !ok {
		return 0.0, fmt.Errorf("invalid non-string argument %v", v)
	}
	if val == "clear" {
		return 0.0, nil
	}
	return 1.0, nil
}

// valueOneOf returns 0.0 if the value matches the first item
// in the array, 1.0 if it matches the second, and an error if
// nothing matches.
func valueOneOf(v interface{}, options []string) (float64, error) {
	val, ok := v.(string)
	if !ok {
		return 0.0, fmt.Errorf("invalid non-string argument %v", v)
	}
	if val == options[0] {
		return 0.0, nil
	}
	if val == options[1] {
		return 1.0, nil
	}
	return 0.0, fmt.Errorf("invalid option %q. Expected %q or %q", val, options[0], options[1])
}

// valueFloat returns the float64 value of the value passed or
// error if the value cannot be converted.
func valueFloat(v interface{}) (float64, error) {
	stringVal, ok := v.(string)
	if ok && stringVal == "" {
		return 0.0, nil
	}
	val, ok := v.(float64)
	if !ok {
		return 0.0, fmt.Errorf("invalid non floating-point argument %v", v)
	}
	return val, nil
}

func init() {
	prometheus.MustRegister(version.NewCollector("smartthings_exporter"))

	registerCommand = application.Command("register", "Register smartthings_exporter with Smartthings and outputs the token.").Action(register)
	registerPort = registerCommand.Flag("register.listen-port", "The port to listen on for the OAuth register.").Default("4567").Uint16()
	registerOAuthClient = registerCommand.Flag("smartthings.oauth-client", "Smartthings OAuth client ID.").Required().String()
	registerOAuthSecret = registerCommand.Flag("smartthings.oauth-secret", "Smartthings OAuth secret key.").Required().String()

	monitorCommand = application.Command("start", "Start the smartthings_exporter.").Default().Action(monitor)
	listenAddress = monitorCommand.Flag("web.listen-address", "Address to listen on for web interface and telemetry.").Default(":9499").String()
	metricsPath = monitorCommand.Flag("web.telemetry-path", "Path under which to expose metrics.").Default("/metrics").String()
	monitorOAuthClient = monitorCommand.Flag("smartthings.oauth-client", "Smartthings OAuth client ID.").Required().String()
	monitorOAuthTokenFile = monitorCommand.Flag("smartthings.oauth-token.file", "File containing the Smartthings OAuth token.").Required().ExistingFile()
}

func main() {
	plog.AddFlags(application)
	application.Version(version.Print("smartthings_exporter"))
	application.HelpFlag.Short('h')
	_, err := application.Parse(os.Args[1:])
	if err != nil {
		application.Fatalf("%s, try --help", err)
	}
}

func register(_ *kingpin.ParseContext) error {
	_, _ = fmt.Fprintln(os.Stderr, "Registering smartthings_exporter with Smartthings")
	config := gosmart.NewOAuthConfig(*registerOAuthClient, *registerOAuthSecret)
	gst, err := gosmart.NewAuth(int(*registerPort), config)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Failed to create Smartthings OAuth client.")
		return err
	}

	_, _ = fmt.Fprintf(os.Stderr, "Please login by visiting: http://localhost:%d\n", *registerPort)
	token, err := gst.FetchOAuthToken()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Failed to fetch Smartthings OAuth token.")
		return err
	}

	blob, err := json.Marshal(token)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Failed to serialize Smartthings OAuth token to JSON.",
			(*registerOAuthTokenFile).Name())
		return err
	}

	fmt.Println(string(blob))
	return nil
}

func monitor(_ *kingpin.ParseContext) error {
	plog.Infoln("Starting smartthings_exporter", version.Info())
	plog.Infoln("Build context", version.BuildContext())

	tokenFilePath, err := filepath.Abs(*monitorOAuthTokenFile)
	if err != nil {
		plog.Errorf("Failed to get absolution path to token file %s.\n", *monitorOAuthTokenFile)
		return err
	}

	token, err := gosmart.LoadToken(tokenFilePath)
	if err != nil || !token.Valid() {
		plog.Errorf("Failed to load Smartthings OAuth token from %s.\n", *monitorOAuthTokenFile)
		return err
	}

	exporter, err := NewExporter(*monitorOAuthClient, token)
	if err != nil {
		plog.Fatalln(err)
		return err
	}
	prometheus.MustRegister(invalidMetric)
	prometheus.MustRegister(unknownMetric)
	prometheus.MustRegister(exporter)

	http.Handle(*metricsPath, promhttp.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>
			        <head><title>SmartThings Exporter</title></head>
			        <body>
			        <h1>SmartThings Exporter</h1>
			        <p><a href='` + *metricsPath + `'>Metrics</a></p>
			        </body>
			        </html>`))
	})

	plog.Infoln("Listening on", *listenAddress)
	plog.Fatal(http.ListenAndServe(*listenAddress, nil))
	return nil
}
