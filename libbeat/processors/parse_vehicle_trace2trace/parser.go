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

package parse_vehicle_trace2trace

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/libbeat/logp"
	"github.com/elastic/beats/v7/libbeat/processors"
)

const (
	procName   = "parse_vehicle_trace2trace"
	logName    = "processor." + procName
	patternStr = "^(\\d{4}\\-\\d{2}\\-\\d{2}\\s\\d{2}:\\d{2}:\\d{2}\\.\\d{3})\\s+(\\d+)\\s+(\\d+)\\s+([a-zA-Z]+)\\s+(.*):\\s*##MSG##\\s*\\[(\\w*)\\]\\s*\\[(\\w*)\\]\\s*\\[(\\w*)\\]\\s*\\[([^\\[\\]]*)\\]\\s*\\[([^\\[\\]]*)\\]\\s+"
)

func init() {
	processors.RegisterPlugin(procName, New)
	// jsprocessor.RegisterPlugin(strings.Title(procName), New)
}

type parseFilebeatLog struct {
	config  Config
	logger  *logp.Logger
	pattern *regexp.Regexp
}

// New constructs a new parse_vehicle_trace2trace processor.
func New(cfg *common.Config) (processors.Processor, error) {
	config := defaultConfig()
	if err := cfg.Unpack(&config); err != nil {
		return nil, makeErrConfigUnpack(err)
	}

	log := logp.NewLogger(logName)

	pattern, err := regexp.Compile(patternStr)
	if err != nil {
		return nil, err
	}
	p := &parseFilebeatLog{
		config:  config,
		logger:  log,
		pattern: pattern,
	}

	return p, nil
}

// Run parse filebeat's log
func (p *parseFilebeatLog) Run(event *beat.Event) (*beat.Event, error) {
	//get the content of log
	msg, err := event.GetValue(p.config.Field)
	if err != nil {
		if p.config.IgnoreMissing {
			return event, nil
		}

		return nil, makeErrMissingField(p.config.Field, err)
	}

	//drop origin field
	if p.config.DropOrigin {
		err := event.Delete(p.config.Field)
		if err != nil {
			p.logger.Warnf("drop event field err: %v", err)
		}
	}

	/* parse */

	message, ok := msg.(string)
	if !ok {
		return nil, makeErrFieldType(p.config.Field, "string", fmt.Sprintf("%T", msg))
	}

	terms := strings.SplitN(message, "\t", 4)
	//Drop logs with incorrect format
	if len(terms) != 4 {
		if p.config.IgnoreMalformed {
			return event, nil
		}

		return nil, makeErrLogFormat("[datetime]\t[LEVEL]\t[hostname]\t[message]")
	}
	_, err = event.PutValue(p.config.TimeField, terms[0])
	if err != nil {
		return nil, makeErrComputeFingerprint(err)
	}

	_, err = event.PutValue("level", strings.ToUpper(terms[1]))
	if err != nil {
		return nil, makeErrComputeFingerprint(err)
	}

	_, err = event.PutValue("hostname", terms[2])
	if err != nil {
		return nil, makeErrComputeFingerprint(err)
	}

	// replace message field
	_, err = event.PutValue("message", terms[3])
	if err != nil {
		return nil, makeErrComputeFingerprint(err)
	}

	return event, nil
}

func (p *parseFilebeatLog) String() string {
	conf, _ := json.Marshal(p.config)
	return procName + "=" + string(conf)
}
