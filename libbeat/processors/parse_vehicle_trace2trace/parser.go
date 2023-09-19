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
	"regexp"
	"strconv"
	"strings"

	"github.com/goccy/go-json"

	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/libbeat/logp"
	"github.com/elastic/beats/v7/libbeat/processors"
	"github.com/elastic/beats/v7/libbeat/processors/parse_common"
)

const (
	procName   = "parse_vehicle_trace2trace"
	logName    = "processor." + procName
	patternStr = "^(\\d{4}\\-\\d{2}\\-\\d{2}\\s\\d{2}:\\d{2}:\\d{2}\\.\\d{3})\\s+(\\d+)\\s+(\\d+)\\s+([a-zA-Z]+)\\s+(.*):\\s*##MSG##\\s*\\[(\\w*)\\]\\s*\\[(\\w*)\\]\\s*\\[(\\w*)\\]\\s*\\[([^\\[\\]]*)\\]\\s*\\[([^\\[\\]]*)\\]\\s+"
)

func init() {
	processors.RegisterPlugin(procName, NewParseVehicleTrace2trace)
	// jsprocessor.RegisterPlugin(strings.Title(procName), New)
}

type parseVehicleTrace2trace struct {
	config  Config
	logger  *logp.Logger
	pattern *regexp.Regexp
}

// NewParseVehicleTrace2trace constructs a new parse_vehicle_trace2trace processor.
func NewParseVehicleTrace2trace(cfg *common.Config) (processors.Processor, error) {
	config := defaultConfig()
	if err := cfg.Unpack(&config); err != nil {
		return nil, makeErrConfigUnpack(err)
	}

	logger := logp.NewLogger(logName)

	pattern, err := regexp.Compile(patternStr)
	if err != nil {
		return nil, err
	}
	p := &parseVehicleTrace2trace{
		config:  config,
		logger:  logger,
		pattern: pattern,
	}

	return p, nil
}

// todo  拆成多个processor
// Run parse log
func (p *parseVehicleTrace2trace) Run(event *beat.Event) (*beat.Event, error) {
	//get the content of log
	message, err := event.GetValue(p.config.Field)
	if err != nil {
		if p.config.IgnoreMissing {
			return event, nil
		}
		return nil, makeErrMissingField(p.config.Field, err)
	}

	var msgObj common.MapStr
	err = json.Unmarshal([]byte(message.(string)), &msgObj)
	if err != nil {
		return nil, err
	}
	msg, err := msgObj.GetValue("message")
	if err != nil {
		return nil, err
	}

	path, err := msgObj.GetValue("log.file.path")
	if err != nil {
		if p.config.IgnoreMissing {
			return event, nil
		}
		return nil, makeErrMissingField("log.file.path", err)
	}

	//drop origin field
	if p.config.DropOrigin {
		err := event.Delete(p.config.Field)
		if err != nil {
			p.logger.Warnf("drop event field err: %v", err)
		}
	}

	/* parse */
	items := strings.Split(path.(string), "@")

	if len(items) == 6 {
		event.Fields["x-header_filename"] = items[0][strings.LastIndex(items[0], "/")+1 : strings.LastIndex(items[0], ".")]
		event.Fields["x-header_ecu"] = items[1]
		event.Fields["x-header_vid"] = items[2]
		event.Fields["x-header_log_type"] = items[3]
		event.Fields["x-header_created_at"] = items[4]
		event.Fields["x-header_uploaded_at"] = items[5]
	}
	msgStr := msg.(string)

	event.Fields["message"] = msgStr
	lists := p.pattern.FindStringSubmatch(msgStr)

	if len(lists) >= 11 && len(lists[6]) > 0 {
		event.Fields["time"] = lists[1]
		pid, err := strconv.ParseInt(lists[2], 10, 64)
		if err != nil {
			pid = 0
		}

		event.Fields["pid"] = pid
		tid, err := strconv.ParseInt(lists[3], 10, 64)
		if err != nil {
			tid = 0
		}
		event.Fields["tid"] = tid
		if value, ok := parse_common.LevelMap[lists[4]]; ok {
			event.Fields["level"] = value
		} else {
			event.Fields["level"] = lists[4]
		}
		event.Fields["tag"] = lists[5]
		event.Fields["trace_id"] = lists[6]
		event.Fields["span_id"] = lists[7]
		event.Fields["parent_span_id"] = lists[8]
		event.Fields["network"] = lists[9]
		event.Fields["user_id"] = lists[10]
		if endIdx := strings.LastIndex(msgStr, "##MSG##"); endIdx > len(lists[0]) {
			event.Fields["message"] = msgStr[len(lists[0]):endIdx]
		} else {
			event.Fields["message"] = msgStr[len(lists[0]):]
		}
	}

	return event, nil
}

func (p *parseVehicleTrace2trace) String() string {
	conf, _ := json.Marshal(p.config)
	return procName + "=" + string(conf)
}
