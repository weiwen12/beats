// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License;
// you may not use this file except in compliance with the Elastic License.

// Code generated by beats/dev-tools/cmd/asset/asset.go - DO NOT EDIT.

package panw

import (
	"github.com/elastic/beats/v7/libbeat/asset"
)

func init() {
	if err := asset.SetFields("filebeat", "panw", asset.ModuleFieldsPri, AssetPanw); err != nil {
		panic(err)
	}
}

// AssetPanw returns asset data.
// This is the base64 encoded zlib format compressed contents of module/panw.
func AssetPanw() string {
	return "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"
}
