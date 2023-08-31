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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/libbeat/common"
)

type ParseResult struct {
	logtime  string
	level    string
	hostname string
	message  string
}

var (
	defaultMessage = "2023-08-31T12:14:43.594+0800\tINFO\tfilebeat-benchmark-log-dev-75c886ff7d-rx9sd\t[monitoring]\tlog/log.go:184\tNon-zero metrics in the last 30s"
)

func TestWithConfig(t *testing.T) {
	cases := map[string]struct {
		config common.MapStr
		input  common.MapStr
		want   ParseResult
	}{
		"default config": {
			config: common.MapStr{},
			input: common.MapStr{
				"message": defaultMessage,
			},
			want: ParseResult{
				logtime:  "2023-08-31T12:14:43.594+0800",
				level:    "INFO",
				hostname: "filebeat-benchmark-log-dev-75c886ff7d-rx9sd",
				message:  "[monitoring]\tlog/log.go:184\tNon-zero metrics in the last 30s",
			},
		},
		"specified message field": {
			config: common.MapStr{
				"field": "custom_message",
			},
			input: common.MapStr{
				"custom_message": defaultMessage,
			},
			want: ParseResult{
				logtime:  "2023-08-31T12:14:43.594+0800",
				level:    "INFO",
				hostname: "filebeat-benchmark-log-dev-75c886ff7d-rx9sd",
				message:  "[monitoring]\tlog/log.go:184\tNon-zero metrics in the last 30s",
			},
		},
		"specified time field": {
			config: common.MapStr{
				"time_field": "custom_time",
			},
			input: common.MapStr{
				"message": defaultMessage,
			},
			want: ParseResult{
				logtime:  "2023-08-31T12:14:43.594+0800",
				level:    "INFO",
				hostname: "filebeat-benchmark-log-dev-75c886ff7d-rx9sd",
				message:  "[monitoring]\tlog/log.go:184\tNon-zero metrics in the last 30s",
			},
		},
	}

	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			config := common.MustNewConfigFrom(test.config)
			p, err := New(config)
			require.NoError(t, err)

			testEvent := &beat.Event{
				Timestamp: time.Unix(1693463968, 0),
				Fields:    test.input.Clone(),
			}
			newEvent, err := p.Run(testEvent)
			require.NoError(t, err)

			processor := p.(*parseFilebeatLog)

			fieldEqual(t, newEvent, processor.config.TimeField, test.want.logtime)
			fieldEqual(t, newEvent, "level", test.want.level)
			fieldEqual(t, newEvent, "hostname", test.want.hostname)
			fieldEqual(t, newEvent, "message", test.want.message)
		})
	}
}

func fieldEqual(t *testing.T, event *beat.Event, field string, expected interface{}) {
	value, err := event.GetValue(field)
	if err != nil {
		t.Error(err)
		return
	}

	assert.Equal(t, expected, value)
}
