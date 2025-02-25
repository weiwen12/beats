// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package instance

import (
	"context"
	cryptRand "crypto/rand"
	"errors"
	"flag"
	"fmt"
	"github.com/goccy/go-json"
	"io"
	"math"
	"math/big"
	"math/rand"
	"net"
	"os"
	"os/user"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/zap"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/elastic/beats/v7/libbeat/api"
	"github.com/elastic/beats/v7/libbeat/asset"
	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/libbeat/cfgfile"
	"github.com/elastic/beats/v7/libbeat/cloudid"
	"github.com/elastic/beats/v7/libbeat/cmd/instance/metrics"
	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/libbeat/common/cfgwarn"
	"github.com/elastic/beats/v7/libbeat/common/file"
	"github.com/elastic/beats/v7/libbeat/common/reload"
	"github.com/elastic/beats/v7/libbeat/common/seccomp"
	"github.com/elastic/beats/v7/libbeat/dashboards"
	"github.com/elastic/beats/v7/libbeat/esleg/eslegclient"
	"github.com/elastic/beats/v7/libbeat/idxmgmt"
	"github.com/elastic/beats/v7/libbeat/instrumentation"
	"github.com/elastic/beats/v7/libbeat/keystore"
	"github.com/elastic/beats/v7/libbeat/kibana"
	"github.com/elastic/beats/v7/libbeat/logp"
	"github.com/elastic/beats/v7/libbeat/logp/configure"
	"github.com/elastic/beats/v7/libbeat/management"
	"github.com/elastic/beats/v7/libbeat/metric/system/host"
	"github.com/elastic/beats/v7/libbeat/monitoring"
	"github.com/elastic/beats/v7/libbeat/monitoring/report"
	"github.com/elastic/beats/v7/libbeat/monitoring/report/log"
	"github.com/elastic/beats/v7/libbeat/outputs"
	"github.com/elastic/beats/v7/libbeat/outputs/elasticsearch"
	"github.com/elastic/beats/v7/libbeat/paths"
	"github.com/elastic/beats/v7/libbeat/plugin"
	"github.com/elastic/beats/v7/libbeat/publisher/pipeline"
	"github.com/elastic/beats/v7/libbeat/publisher/processing"
	svc "github.com/elastic/beats/v7/libbeat/service"
	"github.com/elastic/beats/v7/libbeat/version"
	sysinfo "github.com/elastic/go-sysinfo"
	"github.com/elastic/go-sysinfo/types"
	ucfg "github.com/elastic/go-ucfg"
)

// Beat provides the runnable and configurable instance of a beat.
type Beat struct {
	beat.Beat

	Config       beatConfig
	RawConfig    *common.Config // Raw config that can be unpacked to get Beat specific config data.
	IdxSupporter idxmgmt.Supporter

	keystore   keystore.Keystore
	processing processing.Supporter

	InputQueueSize int // Size of the producer queue used by most queues.
}

type beatConfig struct {
	beat.BeatConfig `config:",inline"`

	// instance internal configs

	// beat top-level settings
	Name      string `config:"name"`
	MaxProcs  int    `config:"max_procs"`
	GCPercent int    `config:"gc_percent"`

	Seccomp *common.Config `config:"seccomp"`

	// beat internal components configurations
	HTTP            *common.Config         `config:"http"`
	HTTPPprof       *common.Config         `config:"http.pprof"`
	Path            paths.Path             `config:"path"`
	Logging         *common.Config         `config:"logging"`
	MetricLogging   *common.Config         `config:"logging.metrics"`
	Keystore        *common.Config         `config:"keystore"`
	Instrumentation instrumentation.Config `config:"instrumentation"`

	// output/publishing related configurations
	Pipeline pipeline.Config `config:",inline"`

	// monitoring settings
	MonitoringBeatConfig monitoring.BeatConfig `config:",inline"`

	// central management settings
	Management *common.Config `config:"management"`

	// elastic stack 'setup' configurations
	Dashboards *common.Config `config:"setup.dashboards"`
	Kibana     *common.Config `config:"setup.kibana"`

	// Migration config to migration from 6 to 7
	Migration *common.Config `config:"migration.6_to_7"`
}

var debugf = logp.MakeDebug("beat")

func init() {
	initRand()
}

// initRand initializes the runtime random number generator seed using
// global, shared cryptographically strong pseudo random number generator.
//
// On linux Reader might use getrandom(2) or /udev/random. On windows systems
// CryptGenRandom is used.
func initRand() {
	n, err := cryptRand.Int(cryptRand.Reader, big.NewInt(math.MaxInt64))
	var seed int64
	if err != nil {
		// fallback to current timestamp
		seed = time.Now().UnixNano()
	} else {
		seed = n.Int64()
	}
	rand.Seed(seed)
}

// Run initializes and runs a Beater implementation. name is the name of the
// Beat (e.g. packetbeat or metricbeat). version is version number of the Beater
// implementation. bt is the `Creator` callback for creating a new beater
// instance.
// XXX Move this as a *Beat method?
func Run(settings Settings, bt beat.Creator) error {
	err := setUmaskWithSettings(settings)
	if err != nil && !errors.Is(err, errNotImplemented) {
		return fmt.Errorf("could not set umask: %w", err)
	}

	return handleError(func() error {
		defer func() {
			if r := recover(); r != nil {
				logp.NewLogger(settings.Name).Fatalw("Failed due to panic.",
					"panic", r, zap.Stack("stack"))
			}
		}()
		b, err := NewInitializedBeat(settings)
		if err != nil {
			return err
		}
		return b.launch(settings, bt)
	}())
}

// NewInitializedBeat creates a new beat where all information and initialization is derived from settings
func NewInitializedBeat(settings Settings) (*Beat, error) {
	b, err := NewBeat(settings.Name, settings.IndexPrefix, settings.Version, settings.ElasticLicensed)
	if err != nil {
		return nil, err
	}
	if err := b.InitWithSettings(settings); err != nil {
		return nil, err
	}
	return b, nil
}

// NewBeat creates a new beat instance
func NewBeat(name, indexPrefix, v string, elasticLicensed bool) (*Beat, error) {
	if v == "" {
		v = version.GetDefaultVersion()
	}
	if indexPrefix == "" {
		indexPrefix = name
	}

	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}

	fields, err := asset.GetFields(name)
	if err != nil {
		return nil, err
	}

	id, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}

	b := beat.Beat{
		Info: beat.Info{
			Beat:            name,
			ElasticLicensed: elasticLicensed,
			IndexPrefix:     indexPrefix,
			Version:         v,
			Name:            hostname,
			Hostname:        hostname,
			ID:              id,
			FirstStart:      time.Now(),
			StartTime:       time.Now(),
			EphemeralID:     metrics.EphemeralID(),
		},
		Fields: fields,
	}

	return &Beat{Beat: b}, nil
}

// InitWithSettings does initialization of things common to all actions (read confs, flags)
func (b *Beat) InitWithSettings(settings Settings) error {
	err := b.handleFlags()
	if err != nil {
		return err
	}

	if err := plugin.Initialize(); err != nil {
		return err
	}

	if err := b.configure(settings); err != nil {
		return err
	}

	return nil
}

// Init does initialization of things common to all actions (read confs, flags)
//
// Deprecated: use InitWithSettings
func (b *Beat) Init() error {
	return b.InitWithSettings(Settings{})
}

// BeatConfig returns config section for this beat
func (b *Beat) BeatConfig() (*common.Config, error) {
	configName := strings.ToLower(b.Info.Beat)
	if b.RawConfig.HasField(configName) {
		sub, err := b.RawConfig.Child(configName, -1)
		if err != nil {
			return nil, err
		}

		return sub, nil
	}

	return common.NewConfig(), nil
}

// Keystore return the configured keystore for this beat
func (b *Beat) Keystore() keystore.Keystore {
	return b.keystore
}

// create and return the beater, this method also initializes all needed items,
// including template registering, publisher, xpack monitoring
func (b *Beat) createBeater(bt beat.Creator) (beat.Beater, error) {
	sub, err := b.BeatConfig()
	if err != nil {
		return nil, err
	}

	logSystemInfo(b.Info)
	logp.Info("Setup Beat: %s; Version: %s", b.Info.Beat, b.Info.Version)

	err = b.registerESIndexManagement()
	if err != nil {
		return nil, err
	}

	err = b.registerClusterUUIDFetching()
	if err != nil {
		return nil, err
	}

	reg := monitoring.Default.GetRegistry("libbeat")
	if reg == nil {
		reg = monitoring.Default.NewRegistry("libbeat")
	}

	err = metrics.SetupMetrics(b.Info.Beat)
	if err != nil {
		return nil, err
	}

	// Report central management state
	mgmt := monitoring.GetNamespace("state").GetRegistry().NewRegistry("management")
	monitoring.NewBool(mgmt, "enabled").Set(b.Manager.Enabled())

	debugf("Initializing output plugins")
	outputEnabled := b.Config.Output.IsSet() && b.Config.Output.Config().Enabled()
	if !outputEnabled {
		if b.Manager.Enabled() {
			logp.Info("Output is configured through Central Management")
		} else {
			msg := "No outputs are defined. Please define one under the output section."
			logp.Info(msg)
			return nil, errors.New(msg)
		}
	}

	var publisher *pipeline.Pipeline
	monitors := pipeline.Monitors{
		Metrics:   reg,
		Telemetry: monitoring.GetNamespace("state").GetRegistry(),
		Logger:    logp.L().Named("publisher"),
		Tracer:    b.Instrumentation.Tracer(),
	}
	outputFactory := b.makeOutputFactory(b.Config.Output)
	settings := pipeline.Settings{
		WaitClose:      0,
		WaitCloseMode:  pipeline.NoWaitOnClose,
		Processors:     b.processing,
		InputQueueSize: b.InputQueueSize,
	}
	if settings.InputQueueSize > 0 {
		publisher, err = pipeline.LoadWithSettings(b.Info, monitors, b.Config.Pipeline, outputFactory, settings)
	} else {
		publisher, err = pipeline.Load(b.Info, monitors, b.Config.Pipeline, b.processing, outputFactory)
	}
	if err != nil {
		return nil, fmt.Errorf("error initializing publisher: %w", err)
	}

	reload.Register.MustRegister("output", b.makeOutputReloader(publisher.OutputReloader()))

	// TODO: some beats race on shutdown with publisher.Stop -> do not call Stop yet,
	//       but refine publisher to disconnect clients on stop automatically
	// defer pipeline.Close()

	b.Publisher = publisher
	beater, err := bt(&b.Beat, sub)
	if err != nil {
		return nil, err
	}

	return beater, nil
}

func (b *Beat) launch(settings Settings, bt beat.Creator) error {
	defer func() {
		_ = logp.Sync()
	}()
	defer logp.Info("%s stopped.", b.Info.Beat)

	defer func() {
		if err := b.processing.Close(); err != nil {
			logp.Warn("Failed to close global processing: %v", err)
		}
	}()

	// Windows: Mark service as stopped.
	// After this is run, a Beat service is considered by the OS to be stopped
	// and another instance of the process can be started.
	// This must be the first deferred cleanup task (last to execute).
	defer svc.NotifyTermination()

	// Try to acquire exclusive lock on data path to prevent another beat instance
	// sharing same data path.
	bl := newLocker(b)
	err := bl.lock()
	if err != nil {
		return err
	}

	defer func() {
		_ = bl.unlock()
	}()

	svc.BeforeRun()
	defer svc.Cleanup()

	b.registerMetrics()

	// Start the API Server before the Seccomp lock down, we do this so we can create the unix socket
	// set the appropriate permission on the unix domain file without having to whitelist anything
	// that would be set at runtime.
	if b.Config.HTTP.Enabled() {
		s, err := api.NewWithDefaultRoutes(logp.NewLogger(""), b.Config.HTTP, monitoring.GetNamespace)
		if err != nil {
			return fmt.Errorf("could not start the HTTP server for the API: %w", err)
		}
		s.Start()
		defer func() {
			_ = s.Stop()
		}()
		if b.Config.HTTPPprof.Enabled() {
			s.AttachPprof()
		}
	}

	if err = seccomp.LoadFilter(b.Config.Seccomp); err != nil {
		return err
	}

	beater, err := b.createBeater(bt)
	if err != nil {
		return err
	}

	r, err := b.setupMonitoring(settings)
	if err != nil {
		return err
	}
	if r != nil {
		defer r.Stop()
	}

	if b.Config.MetricLogging == nil || b.Config.MetricLogging.Enabled() {
		reporter, err := log.MakeReporter(b.Info, b.Config.MetricLogging)
		if err != nil {
			return err
		}
		defer reporter.Stop()
	}

	ctx, cancel := context.WithCancel(context.Background())
	var stopBeat = func() {
		b.Instrumentation.Tracer().Close()
		beater.Stop()
	}
	svc.HandleSignals(stopBeat, cancel)

	err = b.loadDashboards(ctx, false)
	if err != nil {
		return err
	}

	logp.Info("%s start running.", b.Info.Beat)

	// Allow the manager to stop a currently running beats out of bound.
	b.Manager.SetStopCallback(beater.Stop)

	return beater.Run(&b.Beat)
}

// registerMetrics registers metrics with the internal monitoring API. This data
// is then exposed through the HTTP monitoring endpoint (e.g. /info and /state)
// and/or pushed to Elasticsearch through the x-pack monitoring feature.
func (b *Beat) registerMetrics() {
	// info
	infoRegistry := monitoring.GetNamespace("info").GetRegistry()
	monitoring.NewString(infoRegistry, "version").Set(b.Info.Version)
	monitoring.NewString(infoRegistry, "beat").Set(b.Info.Beat)
	monitoring.NewString(infoRegistry, "name").Set(b.Info.Name)
	monitoring.NewString(infoRegistry, "hostname").Set(b.Info.Hostname)
	monitoring.NewString(infoRegistry, "uuid").Set(b.Info.ID.String())
	monitoring.NewString(infoRegistry, "ephemeral_id").Set(b.Info.EphemeralID.String())
	monitoring.NewString(infoRegistry, "binary_arch").Set(runtime.GOARCH)
	monitoring.NewString(infoRegistry, "build_commit").Set(version.Commit())
	monitoring.NewTimestamp(infoRegistry, "build_time").Set(version.BuildTime())
	monitoring.NewBool(infoRegistry, "elastic_licensed").Set(b.Info.ElasticLicensed)

	// Add user metadata data asynchronously (on Windows the lookup can take up to 60s).
	go func() {
		if u, err := user.Current(); err != nil {
			// This usually happens if the user UID does not exist in /etc/passwd. It might be the case on K8S
			// if the user set securityContext.runAsUser to an arbitrary value.
			monitoring.NewString(infoRegistry, "uid").Set(strconv.Itoa(os.Getuid()))
			monitoring.NewString(infoRegistry, "gid").Set(strconv.Itoa(os.Getgid()))
		} else {
			monitoring.NewString(infoRegistry, "username").Set(u.Username)
			monitoring.NewString(infoRegistry, "uid").Set(u.Uid)
			monitoring.NewString(infoRegistry, "gid").Set(u.Gid)
		}
	}()

	stateRegistry := monitoring.GetNamespace("state").GetRegistry()

	// state.service
	serviceRegistry := stateRegistry.NewRegistry("service")
	monitoring.NewString(serviceRegistry, "version").Set(b.Info.Version)
	monitoring.NewString(serviceRegistry, "name").Set(b.Info.Beat)
	monitoring.NewString(serviceRegistry, "id").Set(b.Info.ID.String())

	// state.beat
	beatRegistry := stateRegistry.NewRegistry("beat")
	monitoring.NewString(beatRegistry, "name").Set(b.Info.Name)

	// state.host
	monitoring.NewFunc(stateRegistry, "host", host.ReportInfo, monitoring.Report)
}

// TestConfig check all settings are ok and the beat can be run
func (b *Beat) TestConfig(settings Settings, bt beat.Creator) error {
	return handleError(func() error {
		err := b.InitWithSettings(settings)
		if err != nil {
			return err
		}

		// Create beater to ensure all settings are OK
		_, err = b.createBeater(bt)
		if err != nil {
			return err
		}

		fmt.Println("Config OK") //nolint:forbidigo // required to give feedback to user
		return beat.GracefulExit
	}())
}

// SetupSettings holds settings necessary for beat setup
type SetupSettings struct {
	Dashboard       bool
	MachineLearning bool
	Pipeline        bool
	IndexManagement bool
	//Deprecated: use IndexManagementKey instead
	Template bool
	//Deprecated: use IndexManagementKey instead
	ILMPolicy bool
	SetupAll  bool
}

// Setup registers ES index template, kibana dashboards, ml jobs and pipelines.
func (b *Beat) Setup(settings Settings, bt beat.Creator, setup SetupSettings) error {
	return handleError(func() error {
		err := b.InitWithSettings(settings)
		if err != nil {
			return err
		}

		// Tell the beat that we're in the setup command
		b.InSetupCmd = true

		// Create beater to give it the opportunity to set loading callbacks
		_, err = b.createBeater(bt)
		if err != nil {
			return err
		}

		if setup.IndexManagement || setup.Template || setup.ILMPolicy {
			outCfg := b.Config.Output
			if outCfg.Name() != "elasticsearch" {
				return fmt.Errorf("Index management requested but the Elasticsearch output is not configured/enabled")
			}
			esClient, err := eslegclient.NewConnectedClient(outCfg.Config(), b.Info.Beat)
			if err != nil {
				return err
			}

			var loadTemplate, loadILM = idxmgmt.LoadModeUnset, idxmgmt.LoadModeUnset
			if setup.IndexManagement || setup.Template {
				loadTemplate = idxmgmt.LoadModeOverwrite
			}
			if setup.IndexManagement || setup.ILMPolicy {
				loadILM = idxmgmt.LoadModeEnabled
			}
			m := b.IdxSupporter.Manager(idxmgmt.NewESClientHandler(esClient), idxmgmt.BeatsAssets(b.Fields))
			if ok, warn := m.VerifySetup(loadTemplate, loadILM); !ok {
				fmt.Println(warn) //nolint:forbidigo // required to give feedback to user
			}
			if err = m.Setup(loadTemplate, loadILM); err != nil {
				return err
			}
			fmt.Println("Index setup finished.") //nolint:forbidigo // required to give feedback to user
		}

		if setup.Dashboard && settings.HasDashboards {
			fmt.Println("Loading dashboards (Kibana must be running and reachable)") //nolint:forbidigo // required to give feedback to user
			err = b.loadDashboards(context.Background(), true)

			if err != nil {
				var notFoundErr *dashboards.ErrNotFound
				if errors.As(err, &notFoundErr) {
					fmt.Printf("Skipping loading dashboards, %+v\n", err) //nolint:forbidigo // required to give feedback to user
				} else {
					return err
				}
			} else {
				fmt.Println("Loaded dashboards") //nolint:forbidigo // required to give feedback to user
			}
		}

		if setup.MachineLearning && b.SetupMLCallback != nil {
			c := cases.Title(language.English)
			cfgwarn.Deprecate("8.0.0", "Setting up ML using %v is going to be removed. Please use the ML app to setup jobs.", c.String(b.Info.Beat))
			fmt.Println("Setting up ML using setup --machine-learning is going to be removed in 8.0.0. Please use the ML app instead.\nSee more: https://www.elastic.co/guide/en/machine-learning/current/index.html") //nolint:forbidigo // required to give feedback to user
			fmt.Println("It is not possble to load ML jobs into an Elasticsearch 8.0.0 or newer using the Beat.")                                                                                                      //nolint:forbidigo // required to give feedback to user

			err = b.SetupMLCallback(&b.Beat, !setup.SetupAll, b.Config.Kibana)
			if err != nil {
				return err
			}
		}

		if setup.Pipeline && b.OverwritePipelinesCallback != nil {
			esConfig := b.Config.Output.Config()
			err = b.OverwritePipelinesCallback(esConfig)
			if err != nil {
				return err
			}

			fmt.Println("Loaded Ingest pipelines") //nolint:forbidigo // required to give feedback to user
		}

		return nil
	}())
}

// handleFlags parses the command line flags. It invokes the HandleFlags
// callback if implemented by the Beat.
func (b *Beat) handleFlags() error {
	flag.Parse()
	return cfgfile.HandleFlags()
}

// config reads the configuration file from disk, parses the common options
// defined in BeatConfig, initializes logging, and set GOMAXPROCS if defined
// in the config. Lastly it invokes the Config method implemented by the beat.
func (b *Beat) configure(settings Settings) error {
	var err error

	b.InputQueueSize = settings.InputQueueSize

	cfg, err := cfgfile.Load("", settings.ConfigOverrides)
	if err != nil {
		return fmt.Errorf("error loading config file: %w", err)
	}

	if err := initPaths(cfg); err != nil {
		return err
	}

	// We have to initialize the keystore before any unpack or merging the cloud
	// options.
	store, err := LoadKeystore(cfg, b.Info.Beat)
	if err != nil {
		return fmt.Errorf("could not initialize the keystore: %w", err)
	}

	if settings.DisableConfigResolver {
		common.OverwriteConfigOpts(obfuscateConfigOpts())
	} else {
		// TODO: Allow the options to be more flexible for dynamic changes
		common.OverwriteConfigOpts(configOpts(store))
	}

	instrumentation, err := instrumentation.New(cfg, b.Info.Beat, b.Info.Version)
	if err != nil {
		return err
	}
	b.Beat.Instrumentation = instrumentation

	b.keystore = store
	b.Beat.Keystore = store
	err = cloudid.OverwriteSettings(cfg)
	if err != nil {
		return err
	}

	b.RawConfig = cfg
	err = cfg.Unpack(&b.Config)
	if err != nil {
		return fmt.Errorf("error unpacking config data: %w", err)
	}

	b.Beat.Config = &b.Config.BeatConfig

	if name := b.Config.Name; name != "" {
		b.Info.Name = name
	}

	if err := configure.Logging(b.Info.Beat, b.Config.Logging); err != nil {
		return fmt.Errorf("error initializing logging: %w", err)
	}

	// log paths values to help with troubleshooting
	logp.Info(paths.Paths.String())

	metaPath := paths.Resolve(paths.Data, "meta.json")
	err = b.loadMeta(metaPath)
	if err != nil {
		return err
	}

	logp.Info("Beat ID: %v", b.Info.ID)

	// initialize config manager
	b.Manager, err = management.Factory(b.Config.Management)(b.Config.Management, reload.Register, b.Beat.Info.ID)
	if err != nil {
		return err
	}

	if err := b.Manager.CheckRawConfig(b.RawConfig); err != nil {
		return err
	}

	if maxProcs := b.Config.MaxProcs; maxProcs > 0 {
		logp.Info("Set max procs limit: %v", maxProcs)
		runtime.GOMAXPROCS(maxProcs)
	}
	if gcPercent := b.Config.GCPercent; gcPercent > 0 {
		logp.Info("Set gc percentage to: %v", gcPercent)
		debug.SetGCPercent(gcPercent)
	}

	b.Beat.BeatConfig, err = b.BeatConfig()
	if err != nil {
		return err
	}

	imFactory := settings.IndexManagement
	if imFactory == nil {
		imFactory = idxmgmt.MakeDefaultSupport(settings.ILM)
	}
	b.IdxSupporter, err = imFactory(nil, b.Beat.Info, b.RawConfig)
	if err != nil {
		return err
	}

	processingFactory := settings.Processing
	if processingFactory == nil {
		processingFactory = processing.MakeDefaultBeatSupport(true)
	}
	b.processing, err = processingFactory(b.Info, logp.L().Named("processors"), b.RawConfig)

	return err
}

func (b *Beat) loadMeta(metaPath string) error {
	type meta struct {
		UUID       uuid.UUID `json:"uuid"`
		FirstStart time.Time `json:"first_start"`
	}

	logp.Debug("beat", "Beat metadata path: %v", metaPath)

	f, err := openRegular(metaPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("Beat meta file failed to open: %w", err)
	}

	if err == nil {
		m := meta{}
		if err := json.NewDecoder(f).Decode(&m); err != nil && !errors.Is(err, io.EOF) {
			f.Close()
			return fmt.Errorf("Beat meta file reading error: %w", err)
		}

		f.Close()

		if !m.FirstStart.IsZero() {
			b.Info.FirstStart = m.FirstStart
		}
		valid := m.UUID != uuid.Nil
		if valid {
			b.Info.ID = m.UUID
		}

		if valid && !m.FirstStart.IsZero() {
			return nil
		}
	}

	// file does not exist or ID is invalid or first start time is not defined, let's create a new one

	// write temporary file first
	tempFile := metaPath + ".new"
	f, err = os.OpenFile(tempFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("Failed to create Beat meta file: %w", err)
	}

	encodeErr := json.NewEncoder(f).Encode(meta{UUID: b.Info.ID, FirstStart: b.Info.FirstStart})
	err = f.Sync()
	if err != nil {
		return fmt.Errorf("Beat meta file failed to write: %w", err)
	}

	err = f.Close()
	if err != nil {
		return fmt.Errorf("Beat meta file failed to write: %w", err)
	}

	if encodeErr != nil {
		return fmt.Errorf("Beat meta file failed to write: %w", encodeErr)
	}

	// move temporary file into final location
	err = file.SafeFileRotate(metaPath, tempFile)
	return err
}

func openRegular(filename string) (*os.File, error) {
	f, err := os.Open(filename)
	if err != nil {
		return f, err
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	if !info.Mode().IsRegular() {
		f.Close()
		if info.IsDir() {
			return nil, fmt.Errorf("%s is a directory", filename)
		}
		return nil, fmt.Errorf("%s is not a regular file", filename)
	}

	return f, nil
}

func (b *Beat) loadDashboards(ctx context.Context, force bool) error {
	if force {
		// force implies dashboards.enabled=true
		if b.Config.Dashboards == nil {
			b.Config.Dashboards = common.NewConfig()
		}
		err := b.Config.Dashboards.SetBool("enabled", -1, true)
		if err != nil {
			return fmt.Errorf("Error setting dashboard.enabled=true: %w", err)
		}
	}

	if b.Config.Dashboards.Enabled() {

		// Initialize kibana config. If username and password is set in elasticsearch output config but not in kibana,
		// initKibanaConfig will attach the username and password into kibana config as a part of the initialization.
		kibanaConfig, err := initKibanaConfig(b.Config)
		if err != nil {
			return fmt.Errorf("error initKibanaConfig: %w", err)
		}

		client, err := kibana.NewKibanaClient(kibanaConfig, b.Info.Beat)
		if err != nil {
			return fmt.Errorf("error connecting to Kibana: %w", err)
		}
		// This fetches the version for Kibana. For the alias feature the version of ES would be needed
		// but it's assumed that KB and ES have the same minor version.
		v := client.GetVersion()

		indexPattern, err := kibana.NewGenerator(b.Info.IndexPrefix, b.Info.Beat, b.Fields, b.Info.Version, v, b.Config.Migration.Enabled())
		if err != nil {
			return fmt.Errorf("error creating index pattern generator: %w", err)
		}

		pattern, err := indexPattern.Generate()
		if err != nil {
			return fmt.Errorf("error generating index pattern: %w", err)
		}

		err = dashboards.ImportDashboards(ctx, b.Info, paths.Resolve(paths.Home, ""),
			kibanaConfig, b.Config.Dashboards, nil, pattern)
		if err != nil {
			return fmt.Errorf("Error importing Kibana dashboards: %w", err)
		}
		logp.Info("Kibana dashboards successfully loaded.")
	}

	return nil
}

// registerESIndexManagement registers the loading of the template and ILM
// policy as a callback with the elasticsearch output. It is important the
// registration happens before the publisher is created.
func (b *Beat) registerESIndexManagement() error {
	if b.Config.Output.Name() != "elasticsearch" || !b.IdxSupporter.Enabled() {
		return nil
	}

	_, err := elasticsearch.RegisterConnectCallback(b.indexSetupCallback())
	if err != nil {
		return fmt.Errorf("failed to register index management with elasticsearch: %w", err)
	}
	return nil
}

func (b *Beat) indexSetupCallback() elasticsearch.ConnectCallback {
	return func(esClient *eslegclient.Connection) error {
		m := b.IdxSupporter.Manager(idxmgmt.NewESClientHandler(esClient), idxmgmt.BeatsAssets(b.Fields))
		return m.Setup(idxmgmt.LoadModeEnabled, idxmgmt.LoadModeEnabled)
	}
}

func (b *Beat) makeOutputReloader(outReloader pipeline.OutputReloader) reload.Reloadable {
	return reload.ReloadableFunc(func(config *reload.ConfigWithMeta) error {
		if b.OutputConfigReloader != nil {
			if err := b.OutputConfigReloader.Reload(config); err != nil {
				return err
			}
		}
		return outReloader.Reload(config, b.createOutput)
	})
}

func (b *Beat) makeOutputFactory(
	cfg common.ConfigNamespace,
) func(outputs.Observer) (string, outputs.Group, error) {
	return func(outStats outputs.Observer) (string, outputs.Group, error) {
		out, err := b.createOutput(outStats, cfg)
		return cfg.Name(), out, err
	}
}

func (b *Beat) createOutput(stats outputs.Observer, cfg common.ConfigNamespace) (outputs.Group, error) {
	if !cfg.IsSet() {
		return outputs.Group{}, nil
	}

	return outputs.Load(b.IdxSupporter, b.Info, stats, cfg.Name(), cfg.Config())
}

func (b *Beat) registerClusterUUIDFetching() error {
	if b.Config.Output.Name() == "elasticsearch" {
		callback, err := b.clusterUUIDFetchingCallback()
		if err != nil {
			return err
		}
		_, _ = elasticsearch.RegisterConnectCallback(callback)
	}
	return nil
}

// Build and return a callback to fetch the Elasticsearch cluster_uuid for monitoring
func (b *Beat) clusterUUIDFetchingCallback() (elasticsearch.ConnectCallback, error) {
	stateRegistry := monitoring.GetNamespace("state").GetRegistry()
	elasticsearchRegistry := stateRegistry.NewRegistry("outputs.elasticsearch")
	clusterUUIDRegVar := monitoring.NewString(elasticsearchRegistry, "cluster_uuid")

	callback := func(esClient *eslegclient.Connection) error {
		var response struct {
			ClusterUUID string `json:"cluster_uuid"`
		}

		status, body, err := esClient.Request("GET", "/", "", nil, nil)
		if err != nil {
			return fmt.Errorf("error querying /: %w", err)
		}
		if status > 299 {
			return fmt.Errorf("Error querying /. Status: %d. Response body: %s", status, body)
		}
		err = json.Unmarshal(body, &response)
		if err != nil {
			return fmt.Errorf("Error unmarshaling json when querying /. Body: %s", body)
		}

		clusterUUIDRegVar.Set(response.ClusterUUID)
		return nil
	}

	return callback, nil
}

func (b *Beat) setupMonitoring(settings Settings) (report.Reporter, error) {
	monitoringCfg, reporterSettings, err := monitoring.SelectConfig(b.Config.MonitoringBeatConfig)
	if err != nil {
		return nil, err
	}

	monitoringClusterUUID, err := monitoring.GetClusterUUID(b.Config.MonitoringBeatConfig.Monitoring)
	if err != nil {
		return nil, err
	}

	// Expose monitoring.cluster_uuid in state API
	if monitoringClusterUUID != "" {
		stateRegistry := monitoring.GetNamespace("state").GetRegistry()
		monitoringRegistry := stateRegistry.NewRegistry("monitoring")
		clusterUUIDRegVar := monitoring.NewString(monitoringRegistry, "cluster_uuid")
		clusterUUIDRegVar.Set(monitoringClusterUUID)
	}

	if monitoring.IsEnabled(monitoringCfg) {
		err := monitoring.OverrideWithCloudSettings(monitoringCfg)
		if err != nil {
			return nil, err
		}

		settings := report.Settings{
			DefaultUsername: settings.Monitoring.DefaultUsername,
			Format:          reporterSettings.Format,
			ClusterUUID:     monitoringClusterUUID,
		}
		reporter, err := report.New(b.Info, settings, monitoringCfg, b.Config.Output)
		if err != nil {
			return nil, err
		}
		return reporter, nil
	}

	return nil, nil
}

// handleError handles the given error by logging it and then returning the
// error. If the err is nil or is a GracefulExit error then the method will
// return nil without logging anything.
func handleError(err error) error {
	if err == nil || errors.Is(err, beat.GracefulExit) {
		return nil
	}

	// logp may not be initialized so log the err to stderr too.
	logp.Critical("Exiting: %v", err)
	fmt.Fprintf(os.Stderr, "Exiting: %v\n", err)
	return err
}

// logSystemInfo logs information about this system for situational awareness
// in debugging. This information includes data about the beat, build, go
// runtime, host, and process. If any of the data is not available it will be
// omitted.
func logSystemInfo(info beat.Info) {
	defer logp.Recover("An unexpected error occurred while collecting " +
		"information about the system.")
	log := logp.NewLogger("beat").With(logp.Namespace("system_info"))

	// Beat
	beat := common.MapStr{
		"type": info.Beat,
		"uuid": info.ID,
		"path": common.MapStr{
			"config": paths.Resolve(paths.Config, ""),
			"data":   paths.Resolve(paths.Data, ""),
			"home":   paths.Resolve(paths.Home, ""),
			"logs":   paths.Resolve(paths.Logs, ""),
		},
	}
	log.Infow("Beat info", "beat", beat)

	// Build
	build := common.MapStr{
		"commit":  version.Commit(),
		"time":    version.BuildTime(),
		"version": info.Version,
		"libbeat": version.GetDefaultVersion(),
	}
	log.Infow("Build info", "build", build)

	// Go Runtime
	log.Infow("Go runtime info", "go", sysinfo.Go())

	// Host
	if host, err := sysinfo.Host(); err == nil {
		hostInfo := host.Info()
		hostInfo.IPs = sanitizeIPs(hostInfo.IPs)
		log.Infow("Host info", "host", hostInfo)
	}

	// Process
	if self, err := sysinfo.Self(); err == nil {
		process := common.MapStr{}

		if info, err := self.Info(); err == nil {
			process["name"] = info.Name
			process["pid"] = info.PID
			process["ppid"] = info.PPID
			process["cwd"] = info.CWD
			process["exe"] = info.Exe
			process["start_time"] = info.StartTime
		}

		if proc, ok := self.(types.Seccomp); ok {
			if seccomp, err := proc.Seccomp(); err == nil {
				process["seccomp"] = seccomp
			}
		}

		if proc, ok := self.(types.Capabilities); ok {
			if caps, err := proc.Capabilities(); err == nil {
				process["capabilities"] = caps
			}
		}

		if len(process) > 0 {
			log.Infow("Process info", "process", process)
		}
	}
}

// configOpts returns ucfg config options with a resolver linked to the current keystore.
// TODO: Refactor to allow insert into the config option array without having to redefine everything
func configOpts(store keystore.Keystore) []ucfg.Option {
	return []ucfg.Option{
		ucfg.PathSep("."),
		ucfg.Resolve(keystore.ResolverWrap(store)),
		ucfg.ResolveEnv,
		ucfg.VarExp,
	}
}

// obfuscateConfigOpts disables any resolvers in the configuration, instead we return the field
// reference string directly.
func obfuscateConfigOpts() []ucfg.Option {
	return []ucfg.Option{
		ucfg.PathSep("."),
		ucfg.ResolveNOOP,
	}
}

// LoadKeystore returns the appropriate keystore based on the configuration.
func LoadKeystore(cfg *common.Config, name string) (keystore.Keystore, error) {
	keystoreCfg, _ := cfg.Child("keystore", -1)
	defaultPathConfig := paths.Resolve(paths.Data, fmt.Sprintf("%s.keystore", name))
	return keystore.Factory(keystoreCfg, defaultPathConfig)
}

func initKibanaConfig(beatConfig beatConfig) (*common.Config, error) {
	var esConfig *common.Config
	if beatConfig.Output.Name() == "elasticsearch" {
		esConfig = beatConfig.Output.Config()
	}

	// init kibana config object
	kibanaConfig := beatConfig.Kibana
	if kibanaConfig == nil {
		kibanaConfig = common.NewConfig()
	}

	if esConfig.Enabled() {
		username, _ := esConfig.String("username", -1)
		password, _ := esConfig.String("password", -1)
		api_key, _ := esConfig.String("api_key", -1)

		if !kibanaConfig.HasField("username") && username != "" {
			_ = kibanaConfig.SetString("username", -1, username)
		}
		if !kibanaConfig.HasField("password") && password != "" {
			_ = kibanaConfig.SetString("password", -1, password)
		}
		if !kibanaConfig.HasField("api_key") && api_key != "" {
			_ = kibanaConfig.SetString("api_key", -1, api_key)
		}
	}
	return kibanaConfig, nil
}

func initPaths(cfg *common.Config) error {
	// To Fix the chicken-egg problem with the Keystore and the loading of the configuration
	// files we are doing a partial unpack of the configuration file and only take into consideration
	// the paths field. After we will unpack the complete configuration and keystore reference
	// will be correctly replaced.
	partialConfig := struct {
		Path   paths.Path `config:"path"`
		Hostfs string     `config:"system.hostfs"`
	}{}

	if paths.IsCLISet() {
		cfgwarn.Deprecate("8.0.0", "This flag will be removed in the future and replaced by a config value.")
	}

	if err := cfg.Unpack(&partialConfig); err != nil {
		return fmt.Errorf("error extracting default paths: %w", err)
	}

	// Read the value for hostfs as `system.hostfs`
	// In the config, there is no `path.hostfs`, as we're merely using the path struct to carry the hostfs variable.
	partialConfig.Path.Hostfs = partialConfig.Hostfs

	if err := paths.InitPaths(&partialConfig.Path); err != nil {
		return fmt.Errorf("error setting default paths: %w", err)
	}
	return nil
}

func setUmaskWithSettings(settings Settings) error {
	if settings.Umask != nil {
		return setUmask(*settings.Umask)
	}
	return setUmask(0027) // 0640 for files | 0750 for dirs
}

// every IP address received from `Info()` has a netmask suffix
// which makes every IP address invalid from the validation perspective.
// If this log entry is ingested to a data stream as it is, the event will be dropped.
// We must make sure every address is valid and does not have suffixes
func sanitizeIPs(ips []string) []string {
	validIPs := make([]string, 0, len(ips))
	for _, ip := range ips {
		if ip == "" {
			continue
		}
		trimIndex := strings.LastIndexByte(ip, '/')
		if trimIndex != -1 {
			ip = ip[:trimIndex]
		}
		if net.ParseIP(ip) == nil {
			continue
		}
		validIPs = append(validIPs, ip)
	}
	return validIPs
}
