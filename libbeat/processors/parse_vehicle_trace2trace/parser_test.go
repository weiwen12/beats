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
	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/libbeat/logp"
	"github.com/stretchr/testify/assert"
	"testing"
)

var (
	defaultMessage = " {\"@timestamp\":\"2023-08-26T04:13:30.649Z\",\"@metadata\":{\"beat\":\"filebeat\",\"type\":\"_doc\",\"version\":\"7.9.3\"},\"log\":{\"offset\":126404,\"file\":{\"path\":\"/vlog/cdc/20230826120955_763.log.gz.1695288295082205184@cdc@b974519299bfa3e1faf92e611331aa08@tracelog@1693023196000@1693023204332\"},\"flags\":[\"multiline\"]},\"message\":\"2023-08-26 12:11:47.898 4664 24435 DEBUG com.jidu.media.service:MediaService@MediaService@HttpLogInterceptor:##MSG## [6d3e1573c45f07a1c60c6be4aeb3d2a0] [789f9212a72f683f] [] [5g] [441018276115528658] response url: https://vehiclesvc.jiduapp.cn/api/cpsp/xmly/history/record/album, Response Time-->：2023-08-26 12:11:47 897\\nTraceParent-->：00-6d3e1573c45f07a1c60c6be4aeb3d2a0-789f9212a72f683f-01\\nResponse Result  -->：{\\\"code\\\":0,\\\"msg\\\":\\\"Success\\\",\\\"showMsg\\\":\\\"\\\"} ##MSG##\",\"fields\":{\"servicetype\":\"tracelogcdc\"}}"
)

func TestWithConfig(t *testing.T) {
	input := common.MapStr{
		"message": defaultMessage,
	}
	testConfig, _ := common.NewConfigFrom(map[string]interface{}{
		"Field":           "message",
		"TimeField":       "logtime",
		"IgnoreMissing":   true,
		"IgnoreMalformed": true,
		"DropOrigin":      true,
	})
	actual := getActualValue(t, testConfig, input)
	expected := common.MapStr{
		"header_filename":    "20230826120955_763.log.gz",
		"header_ecu":         "cdc",
		"header_vid":         "b974519299bfa3e1faf92e611331aa08",
		"header_log_type":    "tracelog",
		"header_created_at":  "1693023196000",
		"header_uploaded_at": "1693023204332",
		"time":               "2023-08-26 12:11:47.898",
		"pid":                int64(4664),
		"tid":                int64(24435),
		"level":              "DEBUG",
		"tag":                "com.jidu.media.service:MediaService@MediaService@HttpLogInterceptor",
		"trace_id":           "6d3e1573c45f07a1c60c6be4aeb3d2a0",
		"span_id":            "789f9212a72f683f",
		"parent_span_id":     "",
		"network":            "5g",
		"user_id":            "441018276115528658",
		"message":            "response url: https://vehiclesvc.jiduapp.cn/api/cpsp/xmly/history/record/album, Response Time-->：2023-08-26 12:11:47 897\nTraceParent-->：00-6d3e1573c45f07a1c60c6be4aeb3d2a0-789f9212a72f683f-01\nResponse Result  -->：{\"code\":0,\"msg\":\"Success\",\"showMsg\":\"\"} ",
	}
	//assert.Equal(t, expected.String(), actual.String())
	assert.Equal(t, expected["header_filename"], actual["x-header_filename"])
	assert.Equal(t, expected["header_ecu"], actual["x-header_ecu"])
	assert.Equal(t, expected["header_vid"], actual["x-header_vid"])
	assert.Equal(t, expected["header_log_type"], actual["x-header_log_type"])
	assert.Equal(t, expected["header_created_at"], actual["x-header_created_at"])
	assert.Equal(t, expected["header_uploaded_at"], actual["x-header_uploaded_at"])
	assert.Equal(t, expected["time"], actual["time"])
	assert.Equal(t, expected["pid"], actual["pid"])
	assert.Equal(t, expected["tid"], actual["tid"])
	assert.Equal(t, expected["level"], actual["level"])
	assert.Equal(t, expected["tag"], actual["tag"])
	assert.Equal(t, expected["trace_id"], actual["trace_id"])
	assert.Equal(t, expected["span_id"], actual["span_id"])
	assert.Equal(t, expected["parent_span_id"], actual["parent_span_id"])
	assert.Equal(t, expected["network"], actual["network"])
	assert.Equal(t, expected["user_id"], actual["user_id"])
	assert.Equal(t, expected["message"], actual["message"])

}

func getActualValue(t *testing.T, config *common.Config, input common.MapStr) common.MapStr {
	log := logp.NewLogger("parse_vehicle_trace2trace_test")

	p, err := NewParseVehicleTrace2trace(config)
	if err != nil {
		log.Error("Error initializing decode_json_fields")
		t.Fatal(err)
	}

	actual, _ := p.Run(&beat.Event{Fields: input})
	return actual.Fields
}
