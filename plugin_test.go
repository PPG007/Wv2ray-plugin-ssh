package main

import (
	"testing"
	pb_plugin "wv2ray-plugin-template/plugin"

	"github.com/stretchr/testify/assert"
)

func TestLocales(t *testing.T) {
	plugin := NewTemplatePlugin()
	_, err := plugin.Init(t.Context(), &pb_plugin.EmptyRequest{})
	assert.NoError(t, err)
	resp, err := plugin.GetInfo(t.Context(), &pb_plugin.EmptyRequest{})
	assert.NoError(t, err)
	t.Logf("%+v", resp.Protocols)
}
