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

package parse_filebeat_log

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/libbeat/processors"
)

const (
	procName = "parse_filebeat_log"
)

func init() {
	processors.RegisterPlugin(procName, New)
	// jsprocessor.RegisterPlugin(strings.Title(procName), New)
}

type parseFilebeatLog struct {
	config Config
}

// New constructs a new fingerprint processor.
func New(cfg *common.Config) (processors.Processor, error) {
	config := defaultConfig()
	if err := cfg.Unpack(&config); err != nil {
		return nil, makeErrConfigUnpack(err)
	}

	p := &parseFilebeatLog{
		config: config,
	}

	return p, nil
}

// Run parse filebeat's log
func (p *parseFilebeatLog) Run(event *beat.Event) (*beat.Event, error) {
	msg, err := event.GetValue("message.contents.content")
	if err != nil {
		return nil, makeErrMissingField("message.contents.content", err)
	}

	message, ok := msg.(string)
	if !ok {
		return nil, makeErrFieldType("message.contents.content", "string", fmt.Sprintf("%T", msg))
	}

	terms := strings.SplitN(message, "\t", 4)
	if len(terms) != 4 {
		return nil, makeErrLogFormat("[datetime]\t[LEVEL]\t[hostname]\t[message]")
	}
	_, err = event.PutValue("logtime", terms[0])
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
