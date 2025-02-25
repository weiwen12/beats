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

package fileset

import (
	"fmt"
	"github.com/goccy/go-json"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"

	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/libbeat/kibana"
	"github.com/elastic/beats/v7/libbeat/logp"
	mlimporter "github.com/elastic/beats/v7/libbeat/ml-importer"
	"github.com/elastic/beats/v7/libbeat/paths"
)

var availableMLModules = map[string]string{
	"apache": "access",
	"nginx":  "access",
}

const logName = "modules"

type ModuleRegistry struct {
	configuredFilesets map[string]map[string]struct{} // module -> fileset -> struct{}
	registry           map[string]map[string]*Fileset // module -> fileset -> Fileset
	log                *logp.Logger
}

// newModuleRegistry reads and loads the configured module into the registry.
func newModuleRegistry(modulesPath string,
	moduleConfigs []*ModuleConfig,
	overrides *ModuleOverrides,
	configuredFilesets map[string]map[string]struct{},
	beatInfo beat.Info,
) (*ModuleRegistry, error) {
	reg := ModuleRegistry{
		configuredFilesets: configuredFilesets,
		registry:           map[string]map[string]*Fileset{},
		log:                logp.NewLogger(logName),
	}

	for _, mcfg := range moduleConfigs {
		if mcfg.Enabled != nil && !(*mcfg.Enabled) {
			continue
		}

		// Look for moved modules
		if module, moved := getCurrentModuleName(modulesPath, mcfg.Module); moved {
			reg.log.Warnf("Configuration uses the old name %q for module %q, please update your configuration.", mcfg.Module, module)
			mcfg.Module = module
		}

		reg.registry[mcfg.Module] = map[string]*Fileset{}
		moduleFilesets, err := getModuleFilesets(modulesPath, mcfg.Module)
		if err != nil {
			return nil, fmt.Errorf("error getting filesets for module %s: %v", mcfg.Module, err)
		}

		for _, filesetName := range moduleFilesets {
			var hasOverride bool
			fcfg, exists := mcfg.Filesets[filesetName]
			if !exists {
				fcfg = &FilesetConfig{}
			}

			fcfg, hasOverride, err = applyOverrides(fcfg, mcfg.Module, filesetName, overrides)
			if err != nil {
				return nil, fmt.Errorf("error applying overrides on fileset %s/%s: %v", mcfg.Module, filesetName, err)
			}
			if hasOverride {
				if _, ok := reg.configuredFilesets[mcfg.Module]; !ok {
					reg.configuredFilesets[mcfg.Module] = map[string]struct{}{}
				}

				reg.configuredFilesets[mcfg.Module][filesetName] = struct{}{}
			}

			if fcfg.Enabled != nil && !(*fcfg.Enabled) {
				continue
			}

			fileset, err := New(modulesPath, filesetName, mcfg, fcfg)
			if err != nil {
				return nil, err
			}
			if err = fileset.Read(beatInfo); err != nil {
				return nil, fmt.Errorf("error reading fileset %s/%s: %v", mcfg.Module, filesetName, err)
			}
			reg.registry[mcfg.Module][filesetName] = fileset
		}

		// check that no extra filesets are configured
		for filesetName, fcfg := range mcfg.Filesets {
			if fcfg.Enabled != nil && !(*fcfg.Enabled) {
				continue
			}
			found := false
			for _, name := range moduleFilesets {
				if filesetName == name {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("fileset %s/%s is configured but doesn't exist", mcfg.Module, filesetName)
			}
		}
	}

	return &reg, nil
}

// NewModuleRegistry reads and loads the configured module into the registry.
func NewModuleRegistry(moduleConfigs []*common.Config, beatInfo beat.Info, init bool) (*ModuleRegistry, error) {
	modulesPath := paths.Resolve(paths.Home, "module")

	stat, err := os.Stat(modulesPath)
	if err != nil || !stat.IsDir() {
		log := logp.NewLogger(logName)
		log.Errorf("Not loading modules. Module directory not found: %s", modulesPath)
		return &ModuleRegistry{log: log}, nil // empty registry, no error
	}

	var modulesCLIList []string
	var modulesOverrides *ModuleOverrides
	if init {
		modulesCLIList, modulesOverrides, err = getModulesCLIConfig()
		if err != nil {
			return nil, err
		}
	}
	var mcfgs []*ModuleConfig
	configuredFilesets := map[string]map[string]struct{}{}
	for _, cfg := range moduleConfigs {
		cfg, err = mergePathDefaults(cfg)
		if err != nil {
			return nil, err
		}

		moduleConfig, moduleConfiguredFilests, err := mcfgFromConfig(cfg)
		if err != nil {
			return nil, errors.Wrap(err, "error unpacking module config")
		}

		configuredFilesets[moduleConfig.Module] = moduleConfiguredFilests
		mcfgs = append(mcfgs, moduleConfig)
	}

	mcfgs, err = appendWithoutDuplicates(mcfgs, modulesCLIList)
	if err != nil {
		return nil, err
	}

	return newModuleRegistry(modulesPath, mcfgs, modulesOverrides, configuredFilesets, beatInfo)
}

func mcfgFromConfig(cfg *common.Config) (*ModuleConfig, map[string]struct{}, error) {
	var mcfg ModuleConfig

	err := cfg.Unpack(&mcfg)
	if err != nil {
		return nil, nil, err
	}

	var dict map[string]interface{}

	err = cfg.Unpack(&dict)
	if err != nil {
		return nil, nil, fmt.Errorf("error unpacking module %s in a dict: %v", mcfg.Module, err)
	}

	mcfg.Filesets = map[string]*FilesetConfig{}
	for name, filesetConfig := range dict {
		if name == "module" || name == "enabled" || name == "path" {
			continue
		}

		tmpCfg, err := common.NewConfigFrom(filesetConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating config from fileset %s/%s: %v", mcfg.Module, name, err)
		}

		fcfg, err := NewFilesetConfig(tmpCfg)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating config from fileset %s/%s: %v", mcfg.Module, name, err)
		}
		mcfg.Filesets[name] = fcfg
	}

	// This calls cfg.GetFields() instead of iterating over `dict` keys
	// because cfg.Unpack above doesn't return keys that map to a nil value,
	// but GetFields() returns all keys. We need to observe filesets that
	// don't contain any configuration (all default values).
	configuredFilets := map[string]struct{}{}

	for _, name := range cfg.GetFields() {
		if name == "module" || name == "enabled" || name == "path" {
			continue
		}

		configuredFilets[name] = struct{}{}
	}

	return &mcfg, configuredFilets, nil
}

func getCurrentModuleName(modulePath, module string) (string, bool) {
	moduleConfigPath := filepath.Join(modulePath, module, "module.yml")
	d, err := ioutil.ReadFile(moduleConfigPath)
	if err != nil {
		return module, false
	}

	var moduleConfig struct {
		MovedTo string `yaml:"movedTo"`
	}
	err = yaml.Unmarshal(d, &moduleConfig)
	if err == nil && moduleConfig.MovedTo != "" {
		return moduleConfig.MovedTo, true
	}

	return module, false
}

func getModuleFilesets(modulePath, module string) ([]string, error) {
	module, _ = getCurrentModuleName(modulePath, module)
	fileInfos, err := ioutil.ReadDir(filepath.Join(modulePath, module))
	if err != nil {
		return []string{}, err
	}

	var filesets []string
	for _, fi := range fileInfos {
		if fi.IsDir() {
			// check also that the `manifest.yml` file exists
			_, err = os.Stat(filepath.Join(modulePath, module, fi.Name(), "manifest.yml"))
			if err == nil {
				filesets = append(filesets, fi.Name())
			}
		}
	}

	return filesets, nil
}

func applyOverrides(fcfg *FilesetConfig,
	module, fileset string,
	overrides *ModuleOverrides) (*FilesetConfig, bool, error) {

	if overrides == nil {
		return fcfg, false, nil
	}

	overridesConfigs := overrides.Get(module, fileset)
	if len(overridesConfigs) == 0 {
		return fcfg, false, nil
	}

	config, err := common.NewConfigFrom(fcfg)
	if err != nil {
		return nil, false, fmt.Errorf("error creating vars config object: %v", err)
	}

	toMerge := []*common.Config{config}
	toMerge = append(toMerge, overridesConfigs...)

	resultConfig, err := common.MergeConfigs(toMerge...)
	if err != nil {
		return nil, false, fmt.Errorf("error merging configs: %v", err)
	}

	res, err := NewFilesetConfig(resultConfig)
	if err != nil {
		return nil, false, fmt.Errorf("error unpacking configs: %v", err)
	}

	return res, true, nil
}

// appendWithoutDuplicates appends basic module configuration for each module in the
// modules list, unless the same module is not already loaded.
func appendWithoutDuplicates(moduleConfigs []*ModuleConfig, modules []string) ([]*ModuleConfig, error) {
	if len(modules) == 0 {
		return moduleConfigs, nil
	}

	// built a dictionary with the configured modules
	modulesMap := map[string]bool{}
	for _, mcfg := range moduleConfigs {
		if mcfg.Enabled != nil && !(*mcfg.Enabled) {
			continue
		}
		modulesMap[mcfg.Module] = true
	}

	// add the non duplicates to the list
	for _, module := range modules {
		if _, exists := modulesMap[module]; !exists {
			moduleConfigs = append(moduleConfigs, &ModuleConfig{Module: module})
		}
	}
	return moduleConfigs, nil
}

func (reg *ModuleRegistry) GetInputConfigs() ([]*common.Config, error) {
	var result []*common.Config
	for module, filesets := range reg.registry {
		for name, fileset := range filesets {
			fcfg, err := fileset.getInputConfig()
			if err != nil {
				return result, fmt.Errorf("error getting config for fileset %s/%s: %v",
					module, name, err)
			}
			result = append(result, fcfg)
		}
	}
	return result, nil
}

// InfoString returns the enabled modules and filesets in a single string, ready to
// be shown to the user
func (reg *ModuleRegistry) InfoString() string {
	var result string
	for module, filesets := range reg.registry {
		var filesetNames string
		for name := range filesets {
			if filesetNames != "" {
				filesetNames += ", "
			}
			filesetNames += name
		}
		if result != "" {
			result += ", "
		}
		result += fmt.Sprintf("%s (%s)", module, filesetNames)
	}
	return result
}

// checkAvailableProcessors calls the /_nodes/ingest API and verifies that all processors listed
// in the requiredProcessors list are available in Elasticsearch. Returns nil if all required
// processors are available.
func checkAvailableProcessors(esClient PipelineLoader, requiredProcessors []ProcessorRequirement) error {
	var response struct {
		Nodes map[string]struct {
			Ingest struct {
				Processors []struct {
					Type string `json:"type"`
				} `json:"processors"`
			} `json:"ingest"`
		} `json:"nodes"`
	}
	status, body, err := esClient.Request("GET", "/_nodes/ingest", "", nil, nil)
	if err != nil {
		return fmt.Errorf("error querying _nodes/ingest: %v", err)
	}
	if status > 299 {
		return fmt.Errorf("error querying _nodes/ingest. Status: %d. Response body: %s", status, body)
	}
	err = json.Unmarshal(body, &response)
	if err != nil {
		return fmt.Errorf("error unmarshaling json when querying _nodes/ingest. Body: %s", body)
	}

	var missing []ProcessorRequirement
	for _, requiredProcessor := range requiredProcessors {
		for _, node := range response.Nodes {
			available := false
			for _, availableProcessor := range node.Ingest.Processors {
				if requiredProcessor.Name == availableProcessor.Type {
					available = true
					break
				}
			}
			if !available {
				missing = append(missing, requiredProcessor)
				break
			}
		}
	}

	if len(missing) > 0 {
		var missingPlugins []string
		for _, proc := range missing {
			missingPlugins = append(missingPlugins, proc.Plugin)
		}
		errorMsg := fmt.Sprintf("this module requires the following Elasticsearch plugins: %s. "+
			"You can install them by running the following commands on all the Elasticsearch nodes:",
			strings.Join(missingPlugins, ", "))
		for _, plugin := range missingPlugins {
			errorMsg += fmt.Sprintf("\n    sudo bin/elasticsearch-plugin install %s", plugin)
		}
		return errors.New(errorMsg)
	}

	return nil
}

// LoadML loads the machine-learning configurations into Elasticsearch, if X-Pack is available
func (reg *ModuleRegistry) LoadML(esClient PipelineLoader) error {
	if !mlimporter.IsCompatible(esClient) {
		logp.Info("Skipping loading machine learning jobs because of Elasticsearch version is too new.\nIt must be 7.x for setting up ML using Beats. Please use the Machine Learning UI in Kibana.")
		return nil
	}

	haveXpack, err := mlimporter.HaveXpackML(esClient)
	if err != nil {
		return errors.Errorf("error checking if xpack is available: %v", err)
	}
	if !haveXpack {
		logp.Warn("X-Pack Machine Learning is not enabled")
		return nil
	}

	for module, filesets := range reg.registry {
		for name, fileset := range filesets {
			for _, mlConfig := range fileset.GetMLConfigs() {
				err := mlimporter.ImportMachineLearningJob(esClient, &mlConfig)
				if err != nil {
					return errors.Errorf("error loading ML config from %s/%s: %v", module, name, err)
				}
			}
		}
	}

	return nil
}

// SetupML sets up the machine-learning configurations into Elasticsearch using Kibana, if X-Pack is available
func (reg *ModuleRegistry) SetupML(esClient PipelineLoader, kibanaClient *kibana.Client) error {
	if !mlimporter.IsCompatible(esClient) {
		logp.Info("Skipping loading machine learning jobs because of Elasticsearch version is too new.\nIt must be 7.x for setting up it using Beats. Please use the Machine Learning UI in Kibana.")
		return nil
	}

	haveXpack, err := mlimporter.HaveXpackML(esClient)
	if err != nil {
		return errors.Errorf("Error checking if xpack is available: %v", err)
	}
	if !haveXpack {
		logp.Warn("X-Pack Machine Learning is not enabled")
		return nil
	}

	modules := make(map[string]string)
	if reg.Empty() {
		modules = availableMLModules
	} else {
		for _, module := range reg.ModuleNames() {
			if fileset, ok := availableMLModules[module]; ok {
				modules[module] = fileset
			}
		}
	}

	for module, fileset := range modules {
		// XXX workaround to setup modules after changing the module IDs due to ECS migration
		// the proper solution would be to query available modules, and setup the required ones
		// related issue: https://github.com/elastic/kibana/issues/30934
		module = module + "_ecs"

		prefix := fmt.Sprintf("filebeat-%s-%s-", module, fileset)
		err := mlimporter.SetupModule(kibanaClient, module, prefix)
		if err != nil {
			return errors.Errorf("Error setting up ML for %s: %v", module, err)
		}
	}
	return nil
}

func (reg *ModuleRegistry) Empty() bool {
	count := 0
	for _, filesets := range reg.registry {
		count += len(filesets)
	}
	return count == 0
}

// ModuleNames returns the names of modules in the ModuleRegistry.
func (reg *ModuleRegistry) ModuleNames() []string {
	var modules []string
	for m := range reg.registry {
		modules = append(modules, m)
	}
	return modules
}

// ModuleFilesets return the list of available filesets for the given module
// it returns an empty list if the module doesn't exist
func (reg *ModuleRegistry) ModuleFilesets(module string) ([]string, error) {
	modulesPath := paths.Resolve(paths.Home, "module")
	return getModuleFilesets(modulesPath, module)
}

// ModuleConfiguredFilesets return the map of configured filesets for the given module
// it returns an empty map if the module doesn't exist
func (reg *ModuleRegistry) ModuleConfiguredFilesets(module string) map[string]struct{} {
	if _, ok := reg.configuredFilesets[module]; ok {
		return reg.configuredFilesets[module]
	}

	return map[string]struct{}{}
}
