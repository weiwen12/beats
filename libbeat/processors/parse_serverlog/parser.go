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

package parse_serverlog

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/goccy/go-json"

	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/libbeat/logp"
	"github.com/elastic/beats/v7/libbeat/processors"
)

const (
	procName = "parse_serverlog"
	logName  = "processor." + procName
)

var (
	jiduservicenamePattern  = regexp.MustCompile(`^[a-z]+[a-z0-9\-\_\.]+$`)
	benchmarkTraceIdPattern = regexp.MustCompile(`^00000000[1-9a-f]`)
)

func init() {
	processors.RegisterPlugin(procName, NewParseServerlog)
	// jsprocessor.RegisterPlugin(strings.Title(procName), New)
}

type parseServerlog struct {
	config Config
	logger *logp.Logger
}

// NewParseServerlog constructs a new parse_serverlog processor.
func NewParseServerlog(cfg *common.Config) (processors.Processor, error) {
	config := defaultConfig()
	if err := cfg.Unpack(&config); err != nil {
		return nil, makeErrConfigUnpack(err)
	}

	logger := logp.NewLogger(logName)

	p := &parseServerlog{
		config: config,
		logger: logger,
	}

	return p, nil
}

// Run parse log
func (p *parseServerlog) Run(event *beat.Event) (*beat.Event, error) {
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

	msg, err := msgObj.GetValue("contents.content")
	if err != nil {
		if p.config.IgnoreMissing {
			return event, nil
		}
		return nil, makeErrMissingField("contents.content", err)
	}
	event.Fields["source_tags"] = msgObj["tags"]
	event.Fields["source_time"] = msgObj["time"]

	msgStr := msg.(string)
	event.Fields[p.config.TimeField] = msgStr[0:23]

	items := strings.SplitN(msgStr, " ", 12)
	if len(items) < 11 {
		return event, nil
	}

	jiduservicename := strings.Replace(items[2], ",", "", 1)
	if !jiduservicenamePattern.MatchString(jiduservicename) {
		return nil, err
	}
	event.Fields["jiduservicename"] = jiduservicename

	//filter benchmark log
	if items[9] != "" && benchmarkTraceIdPattern.MatchString(p.trim(items[9])) {
		return event, nil
	}

	line, err := strconv.ParseInt(p.trim(items[8]), 10, 64)
	idx := strings.Index(msgStr, "##JIDU##")
	if err != nil {
		event.Fields["script_error"] = err
	} else {
		event.Fields["hostname"] = items[3]
		if len(items[4]) > 0 {
			event.Fields["level"] = strings.ToUpper(items[4])
		} else {
			event.Fields["level"] = ""
		}
		event.Fields["thread"] = p.trim(items[5])
		event.Fields["class"] = items[6]
		event.Fields["method"] = items[7]
		event.Fields["line"] = line
		event.Fields["trace_id"] = p.trim(items[9])
		event.Fields["span_id"] = p.trim(items[10])
		if idx >= 0 {
			event.Fields["message"] = msgStr[idx:]
		}
	}

	idx2 := strings.LastIndex(msgStr, "##JIDU##")
	if idx != -1 && idx != idx2 {
		data := msgStr[idx+8 : idx2]
		var obj map[string]interface{}
		err = json.Unmarshal([]byte(data), &obj)
		if err != nil {
			event.Fields["json_error"] = err
		} else {
			for s, i := range obj {
				event.Fields[s] = i
			}
		}
	}

	return event, nil
}

func (p *parseServerlog) trim(s string) string {
	if s == "" || len(s) < 2 {
		return s
	}
	return s[1 : len(s)-1]
}

func (p *parseServerlog) String() string {
	conf, _ := json.Marshal(p.config)
	return procName + "=" + string(conf)
}
