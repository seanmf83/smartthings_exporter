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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	plog "github.com/prometheus/common/log"
	"github.com/prometheus/common/version"
	"github.com/seanmf83/gosmart"
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
	valLockedUnlocked = []string{"locked", "unlocked"}
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
	droppedMetric = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "smartthings_dropped_metric",
			Help: "Total number of metrics that exporter purposely dropped.",
		},
	)
	metricsToDrop = map[string]string{
		"DeviceWatch-DeviceStatus": "stuff here",
		"DeviceWatch-Enroll":       "stuff here",
		"numberOfButtons":          "stuff here",
		"color":                    "stuff here",
		"colorName":                "stuff here",
		"button":                   "stuff here",
		"indicatorStatus":          "stuff here",

		"supportedButtonValues": "stuff here",
		"bulbTemp":              "stuff here",

		"status":       "stuff here",
		"threeAxis":    "stuff here",
		"acceleration": "stuff here",
		"door":         "stuff here",

		// Rachio (General)
		"curZoneIsCycling":  "stuff here",
		"curZoneCycleCount": "stuff here",
		"controllerOn":      "stuff here",
		"rainDelay":         "stuff here",
		"curZoneNumber":     "stuff here",
		"curZoneWaterTime":  "stuff here",
		"rainDelayStr":      "stuff here",
		"hardwareModel":     "stuff here",
		"hardwareDesc":      "stuff here",
		"activeZoneCnt":     "stuff here",
		"curZoneRunStatus":  "stuff here",
		"standbyMode":       "stuff here",
		"curZoneName":       "stuff here",
		"curZoneDuration":   "stuff here",
		"curZoneStartDate":  "stuff here",

		// Rachio (Valves)
		"zoneSquareFeet":           "stuff here",
		"efficiency":               "stuff here",
		"indicashadeNametorStatus": "stuff here",
		"zoneName":                 "stuff here",
		"saturatedDepthOfWater":    "stuff here",
		"zoneNumber":               "stuff here",
		"watering":                 "stuff here",
		"zoneTotalDuration":        "stuff here",
		"rootZoneDepth":            "stuff here",
		"zoneWaterTime":            "stuff here",
		"depthOfWater":             "stuff here",
		"zoneElapsed":              "stuff here",
		"slopeName":                "stuff here",
		"cropName":                 "stuff here",
		"availableWater":           "stuff here",
		"nozzleName":               "stuff here",
		"maxRuntime":               "stuff here",
		"zoneDuration":             "stuff here",
		"zoneStartDate":            "stuff here",
		"zoneCycleCount":           "stuff here",
		"inStandby":                "stuff here",
		"lastUpdatedDt":            "stuff here",
		"scheduleType":             "stuff here",
		"shadeName":                "stuff here",
		"valve":                    "stuff here",
		"soilName":                 "stuff here",

		// DLINK Cam Stuff
		"image":         "stuff here",
		"statusMessage": "stuff here",
		"mute":          "stuff here",
		"hubactionMode": "stuff here",
		"switch2":       "stuff here",
		"switch3":       "stuff here",
		"switch4":       "stuff here",
		"switch5":       "stuff here",
		"switch6":       "stuff here",
		"captureTime":   "stuff here",
		"camera":        "stuff here",
		"settings":      "stuff here",
		"stream":        "stuff here",
		"clip":          "stuff here",

		// Arlo Cams Stuff
		"nightVision":        "stuff here",
		"powerManagement":    "stuff here",
		"desiredCameraState": "stuff here",
		"ruleId":             "stuff here",
		"sound":              "stuff here",
		"invertImage":        "stuff here",
		"offline":            "stuff here",
		"rssi":               "stuff here",
		"active":             "stuff here",
		"timeLastRefresh":    "stuff here",
		"lqi":                "stuff here",
		"clipStatus":         "stuff here",

		// Room Stuff
		"occupancy":        "stuff here",
		"occupancyIconURL": "stuff here",
		"countdown":        "stuff here",

		// Multisensor Stuff
		"batteryStatus": "stuff here",
		"tamper":        "stuff here",
		"powerSource":   "stuff here",
	}
	metrics = map[string]*metric{
		"alarm": {prometheus.NewDesc(prometheus.BuildFQName(namespace, "", "alarm"),
			"1 if the alarm is on.", []string{"id", "name"}, nil),
			func(i interface{}) (f float64, e error) {
				return valueOneOf(i, valOffOn)
			}},

		"alarmState": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "alarm_cleared"), "0 if the alarm is clear.",
			[]string{"id", "name"}, nil), valueClear},

		"battery": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "battery_percentage"),
			"Percentage of battery remaining.", []string{"id", "name"}, nil), valueFloat},

		// TODO fix this duplication
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

		"humidity": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "humidity_level"),
			"Humidity Level.", []string{"id", "name"}, nil), valueFloat},

		"fanSpeed": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "fan_level"),
			"Fan Level.", []string{"id", "name"}, nil), valueFloat},

		"illuminance": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "lux_level"),
			"LUX Level.", []string{"id", "name"}, nil), valueFloat},

		"level": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "level_percent"),
			"Level.", []string{"id", "name"}, nil), valueFloat},

		"lock": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "locked"),
			"Is Locked.", []string{"id", "name"}, nil),
			func(i interface{}) (f float64, e error) {
				return valueOneOf(i, valLockedUnlocked)
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

		"pressure": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "pressure_pascals"),
			"Current pressure in pascals.", []string{"id", "name"}, nil), valueFloat},

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

		"ultravioletIndex": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "ultraviolet_index"),
			"Ultraviolet Index.", []string{"id", "name"}, nil), valueFloat},

		// Tesla Stuff
		"speed": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "speed_miles_per_hour"),
			"Speed at Miles Per Hour.", []string{"id", "name"}, nil), valueFloat},

		"heading": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "heading"),
			"heading.", []string{"id", "name"}, nil), valueFloat},

		"longitude": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "longitude"),
			"longitude.", []string{"id", "name"}, nil), valueFloat},

		"latitude": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "latitude"),
			"latitude.", []string{"id", "name"}, nil), valueFloat},

		"odometer": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "odometer"),
			"odometer.", []string{"id", "name"}, nil), valueFloat},

		"batteryRange": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "battery_range"),
			"Range in Miles for Battery.", []string{"id", "name"}, nil), valueFloat},

		// TBD
		"healthStatus": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "healthStatus"),
			"Health Status.", []string{"id", "name"}, nil), valueFloat},

		"hue": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "hue"),
			"Lighting Hue.", []string{"id", "name"}, nil), valueFloat},

		"saturation": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "saturation"),
			"Lighting Saturation.", []string{"id", "name"}, nil), valueFloat},

		"whiteLevel": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "whiteLevel"),
			"White Light Level.", []string{"id", "name"}, nil), valueFloat},

		"checkInterval": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "checkInterval"),
			"Check Interval.", []string{"id", "name"}, nil), valueFloat},

		"colorTemperature": {prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "colorTemperature"),
			"Color Temperature.", []string{"id", "name"}, nil), valueFloat},
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

// Describe describes all the metrics ever exported by the SmartThings exporter. It
// implements prometheus.Collector.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range metrics {
		ch <- m.description
	}
}

// Collect fetches the stats from configured SmartThings location and delivers them
// as Prometheus metrics. It implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	// Iterate over all devices and collect timeseries info.
	devs, err := gosmart.GetAllDevices(e.client, e.endpoint)
	if err != nil {
		plog.Errorf("Error reading list of devices from %v: %v\n", e.endpoint, err)
	}

	for _, dev := range devs {
		plog.Debugf("Dev> %s Id:%s - Fetching Attributes => %d\n", dev.DisplayName, dev.ID, len(dev.Attributes))

		for k, val := range dev.Attributes {
			if val == nil {
				val = ""
			}

			var value float64
			toDrop := metricsToDrop[k]
			if toDrop != "" {
				droppedMetric.Inc()
				plog.Debugf("  Attr> '%s' [val=%v] - Dropped", k, val)
				continue
			}

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
	prometheus.MustRegister(droppedMetric)
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
