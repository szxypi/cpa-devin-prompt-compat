package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"gopkg.in/yaml.v3"
)

const pluginName = "cpa-devin-prompt-compat"
const pluginVersion = "0.1.1"

type config struct {
	Enabled      bool          `yaml:"enabled"`
	Models       []string      `yaml:"models"`
	LogStats     bool          `yaml:"log-stats"`
	Replacements []replacement `yaml:"replacements"`
}

func defaultConfig() config {
	return config{
		Enabled:  true,
		Models:   []string{"devin/*"},
		LogStats: true,
		Replacements: []replacement{
			{
				From: "You are a Claude agent, built on Anthropic's Claude Agent SDK.",
				To:   "You are an agent built with the Claude Agent SDK.",
			},
			{
				From: "For clear communication with the user the assistant MUST avoid using emojis.",
				To:   "Avoid emojis so that communication with the user stays clear.",
			},
		},
	}
}

var settings atomic.Pointer[config]

func init() {
	c := defaultConfig()
	settings.Store(&c)
}
func main() { fmt.Println(pluginName, pluginVersion) }

func handleMethod(method string, raw []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		var request struct {
			ConfigYAML []byte `json:"config_yaml"`
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, fmt.Errorf("invalid lifecycle request")
			}
		}
		// 从默认配置出发；yaml.Unmarshal 只会覆盖 YAML 中出现的字段，
		// 所以没写 "replacements" 就保留内置默认值，写了就整体覆盖。
		c := defaultConfig()
		if err := yaml.Unmarshal(request.ConfigYAML, &c); err != nil {
			return nil, fmt.Errorf("invalid plugin configuration")
		}
		settings.Store(&c)
		return okEnvelope(struct {
			SchemaVersion uint32             `json:"schema_version"`
			Metadata      pluginapi.Metadata `json:"metadata"`
			Capabilities  map[string]bool    `json:"capabilities"`
		}{pluginabi.SchemaVersion, pluginapi.Metadata{
			Name: pluginName, Version: pluginVersion, Author: "Scottio",
			// CPA 要求填写该字段。这里指向宿主 SDK 仓库，不代表本插件已发布。
			GitHubRepository: "https://github.com/router-for-me/CLIProxyAPI",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "enabled", Type: pluginapi.ConfigFieldTypeBoolean, Description: "是否对匹配到的模型请求启用系统提示词字面替换。"},
				{Name: "models", Type: pluginapi.ConfigFieldTypeArray, Description: "匹配请求模型名的通配符列表（path.Match 语义），例如 devin/*。"},
				{Name: "log-stats", Type: pluginapi.ConfigFieldTypeBoolean, Description: "命中替换时是否记录替换次数和请求/结果字节数。"},
				{Name: "replacements", Type: pluginapi.ConfigFieldTypeArray, Description: "对原始请求体做的 {from,to} 字面替换列表；配置了就完全覆盖默认值。"},
			},
		}, map[string]bool{"request_interceptor": true}})
	case pluginabi.MethodRequestInterceptBefore:
		var request pluginapi.RequestInterceptRequest
		if json.Unmarshal(raw, &request) != nil {
			return okEnvelope(pluginapi.RequestInterceptResponse{})
		}
		c := *settings.Load()
		if !c.Enabled {
			return okEnvelope(pluginapi.RequestInterceptResponse{})
		}
		model := request.RequestedModel
		if model == "" {
			model = request.Model
		}
		out, count := rewrite(request.Body, model, c.Models, c.Replacements)
		if count == 0 {
			return okEnvelope(pluginapi.RequestInterceptResponse{})
		}
		if c.LogStats {
			fmt.Fprintf(os.Stderr, "[%s] replaced=%d request_bytes=%d result_bytes=%d model=%s\n", pluginName, count, len(request.Body), len(out), model)
		}
		return okEnvelope(pluginapi.RequestInterceptResponse{Body: out})
	case pluginabi.MethodRequestInterceptAfter, pluginabi.MethodPluginQuiesce, pluginabi.MethodPluginShutdown:
		return okEnvelope(pluginapi.RequestInterceptResponse{})
	default:
		return errorEnvelope("unknown_method", "unsupported method"), nil
	}
}

func okEnvelope(result any) ([]byte, error) {
	return json.Marshal(struct {
		OK     bool `json:"ok"`
		Result any  `json:"result"`
	}{true, result})
}

func errorEnvelope(code, message string) []byte {
	out, _ := json.Marshal(map[string]any{"ok": false, "error": map[string]string{"code": code, "message": message}})
	return out
}
