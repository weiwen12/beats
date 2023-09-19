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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/libbeat/logp"
)

var (
	defaultMessage = `{"contents":{"content":"2023-09-18 11:32:58.511 ai-repair-common ai-repair-common-69685c846c-kr47m INFO [http-nio-8080-exec-1] com.jidu.postsale.config.LogAspect doAround [66] [4652dc92fb8240777ad468f1623aaaff] [f9567a128ed25419] 【智能维修】【响应日志】{\"code\":0,\"msg\":\"请求成功\"}##JIDU##{\"conts\":{\"cont\":\"123\"},\"ta\":{\"ip\":\"10.90.33.11\",\"name\":\"10.90.33.11\"},\"time-test\":1695007978}##JIDU##"},"tags":{"container.image.name":"docker.jidudev.com/tech/ai-repair-common:s.95f66.57.1904","container.ip":"10.90.44.137","container.name":"ai-repair-common","host.ip":"10.90.162.80","host.name":"log-collector-6s7vk","k8s.namespace.name":"develop","k8s.node.ip":"10.90.33.11","k8s.node.name":"10.90.33.11","k8s.pod.name":"ai-repair-common-69685c846c-kr47m","k8s.pod.uid":"fc75c40f-f5b1-4e64-8ef9-0557c7ceca82","log.file.path":"/app/logs/ai-repair-common/serverlog.ai-repair-common-69685c846c-kr47m.log"},"time":1695007978}`
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
		"source_tags":     `{"container.image.name":"docker.jidudev.com/tech/ai-repair-common:s.95f66.57.1904","container.ip":"10.90.44.137","container.name":"ai-repair-common","host.ip":"10.90.162.80","host.name":"log-collector-6s7vk","k8s.namespace.name":"develop","k8s.node.ip":"10.90.33.11","k8s.node.name":"10.90.33.11","k8s.pod.name":"ai-repair-common-69685c846c-kr47m","k8s.pod.uid":"fc75c40f-f5b1-4e64-8ef9-0557c7ceca82","log.file.path":"/app/logs/ai-repair-common/serverlog.ai-repair-common-69685c846c-kr47m.log"}`,
		"source_time":     int64(1695007978),
		"logtime":         "2023-09-18 11:32:58.511",
		"jiduservicename": "ai-repair-common",
		"hostname":        "ai-repair-common-69685c846c-kr47m",
		"level":           "INFO",
		"thread":          "http-nio-8080-exec-1",
		"class":           "com.jidu.postsale.config.LogAspect",
		"method":          "doAround",
		"line":            int64(66),
		"trace_id":        "4652dc92fb8240777ad468f1623aaaff",
		"span_id":         "f9567a128ed25419",
		"message":         "##JIDU##{\"conts\":{\"cont\":\"123\"},\"ta\":{\"ip\":\"10.90.33.11\",\"name\":\"10.90.33.11\"},\"time-test\":1695007978}##JIDU##",
		"conts.cont":      "123",
		"ta.name":         "10.90.33.11",
		"ta.ip":           "10.90.33.11",
		"time-test":       int64(1695007978),
	}
	//assert.Equal(t, expected.String(), actual.String())
	assert.Equal(t, expected["source_tags"], actual["source_tags"])
	assert.Equal(t, expected["source_time"], actual["source_time"])
	assert.Equal(t, expected["logtime"], actual["logtime"])
	assert.Equal(t, expected["jiduservicename"], actual["jiduservicename"])
	assert.Equal(t, expected["hostname"], actual["hostname"])
	assert.Equal(t, expected["level"], actual["level"])
	assert.Equal(t, expected["thread"], actual["thread"])
	assert.Equal(t, expected["class"], actual["class"])
	assert.Equal(t, expected["method"], actual["method"])
	assert.Equal(t, expected["line"], actual["line"])
	assert.Equal(t, expected["trace_id"], actual["trace_id"])
	assert.Equal(t, expected["span_id"], actual["span_id"])
	assert.Equal(t, expected["message"], actual["message"])
	assert.Equal(t, expected["conts.cont"], actual["conts.cont"])
	assert.Equal(t, expected["ta.name"], actual["ta.name"])
	assert.Equal(t, expected["ta.ip"], actual["ta.ip"])
	assert.Equal(t, expected["time-test"], actual["time-test"])

}

func getActualValue(t *testing.T, config *common.Config, input common.MapStr) common.MapStr {
	log := logp.NewLogger("parse_serverlog_test")

	p, err := NewParseServerlog(config)
	if err != nil {
		log.Error("Error initializing decode_json_fields")
		t.Fatal(err)
	}

	actual, _ := p.Run(&beat.Event{Fields: input})
	return actual.Fields
}
