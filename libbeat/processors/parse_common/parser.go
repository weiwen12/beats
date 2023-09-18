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

package parse_common

import (
	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/libbeat/logp"
	"github.com/elastic/beats/v7/libbeat/processors"
	"github.com/goccy/go-json"
)

const (
	procName = "parse_common"
	logName  = "processor." + procName
)

var (
	LevelMap = map[string]string{
		"V": "VERBOSE",
		"D": "DEBUG",
		"I": "INFO",
		"W": "WARN",
		"E": "ERROR",
		"F": "FATAL",
	}
)

func init() {
	processors.RegisterPlugin(procName, NewParseVehicleTrace2trace)
	// jsprocessor.RegisterPlugin(strings.Title(procName), New)
}

type parseVehicleTrace2trace struct {
	config Config
	logger *logp.Logger
}

// NewParseVehicleTrace2trace constructs a new parse_vehicle_trace2trace processor.
func NewParseVehicleTrace2trace(cfg *common.Config) (processors.Processor, error) {
	config := defaultConfig()
	if err := cfg.Unpack(&config); err != nil {
		return nil, makeErrConfigUnpack(err)
	}

	logger := logp.NewLogger(logName)

	p := &parseVehicleTrace2trace{
		config: config,
		logger: logger,
	}

	return p, nil
}

// Run parse log
func (p *parseVehicleTrace2trace) Run(event *beat.Event) (*beat.Event, error) {
	//get the content of log

	return event, nil
}

func (p *parseVehicleTrace2trace) String() string {
	conf, _ := json.Marshal(p.config)
	return procName + "=" + string(conf)
}
