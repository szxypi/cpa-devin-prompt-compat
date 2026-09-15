package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func claudeFixture() []byte {
	body := map[string]any{
		"model": "devin/claude-code",
		"system": []map[string]any{
			{"type": "text", "text": "You are a Claude agent, built on Anthropic's Claude Agent SDK.\nFor clear communication with the user the assistant MUST avoid using emojis."},
		},
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}
	b, _ := json.Marshal(body)
	return b
}

func TestRewriteDefaultReplacesBothSentences(t *testing.T) {
	c := defaultConfig()
	out, count := rewrite(claudeFixture(), "devin/claude-code", c.Models, c.Replacements)
	if count != 2 {
		t.Fatalf("count=%d, want 2", count)
	}
	if !json.Valid(out) {
		t.Fatal("result is not valid JSON")
	}
	var decoded struct {
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	text := decoded.System[0].Text
	if want := "You are an agent built with the Claude Agent SDK."; !contains(text, want) {
		t.Fatalf("missing replaced identity sentence, got: %s", text)
	}
	if want := "Avoid emojis so that communication with the user stays clear."; !contains(text, want) {
		t.Fatalf("missing replaced emoji sentence, got: %s", text)
	}
	if contains(text, "Claude Agent SDK.\nFor clear communication") {
		t.Fatal("original sentences still present")
	}
}

func TestRewriteModelMismatchNoop(t *testing.T) {
	c := defaultConfig()
	body := claudeFixture()
	out, count := rewrite(body, "gpt-5.6", c.Models, c.Replacements)
	if count != 0 {
		t.Fatalf("count=%d, want 0", count)
	}
	if string(out) != string(body) {
		t.Fatal("body changed despite model mismatch")
	}
}

func TestRewriteCustomReplacementsOverrideDefaults(t *testing.T) {
	custom := []replacement{{From: "hello", To: "hi there"}}
	out, count := rewrite(claudeFixture(), "devin/claude-code", []string{"devin/*"}, custom)
	if count != 1 {
		t.Fatalf("count=%d, want 1", count)
	}
	if !contains(string(out), "hi there") {
		t.Fatal("custom replacement not applied")
	}
	// Default sentences must remain untouched since custom replacements override defaults entirely.
	if !contains(string(out), "You are a Claude agent, built on Anthropic's Claude Agent SDK.") {
		t.Fatal("default replacement should not have been applied when custom list is set")
	}
}

func TestRewriteEscapedCharacters(t *testing.T) {
	from := "She said \"hi\"\nand left."
	to := "She said \"bye\"\nand stayed."
	body, _ := json.Marshal(map[string]any{
		"model": "devin/claude-code",
		"text":  from,
	})
	out, count := rewrite(body, "devin/claude-code", []string{"devin/*"}, []replacement{{From: from, To: to}})
	if count != 1 {
		t.Fatalf("count=%d, want 1", count)
	}
	if !json.Valid(out) {
		t.Fatal("result is not valid JSON")
	}
	var decoded struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if decoded.Text != to {
		t.Fatalf("text=%q, want %q", decoded.Text, to)
	}
}

func TestMatchModelGlob(t *testing.T) {
	cases := []struct {
		model    string
		patterns []string
		want     bool
	}{
		{"devin/claude-code", []string{"devin/*"}, true},
		{"devin/foo/bar", []string{"devin/*"}, false}, // path.Match "*" does not cross "/"
		{"gpt-5.6", []string{"devin/*"}, false},
		{"", []string{"devin/*"}, false},
		{"devin/x", []string{}, false},
	}
	for _, tc := range cases {
		if got := matchModel(tc.model, tc.patterns); got != tc.want {
			t.Errorf("matchModel(%q, %v) = %v, want %v", tc.model, tc.patterns, got, tc.want)
		}
	}
}

func TestLifecycleAndProtocol(t *testing.T) {
	defer func() { c := defaultConfig(); settings.Store(&c) }()

	// register with defaults
	if _, err := handleMethod(pluginabi.MethodPluginRegister, mustJSON(map[string]any{"config_yaml": []byte("")})); err != nil {
		t.Fatal(err)
	}
	request := pluginapi.RequestInterceptRequest{RequestedModel: "devin/claude-code", Body: claudeFixture()}
	call := func() bool {
		raw, _ := json.Marshal(request)
		out, err := handleMethod(pluginabi.MethodRequestInterceptBefore, raw)
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Result struct {
				Body []byte `json:"Body"`
			} `json:"result"`
		}
		if err := json.Unmarshal(out, &envelope); err != nil {
			t.Fatal(err)
		}
		return len(envelope.Result.Body) > 0
	}
	if !call() {
		t.Fatal("matching devin model with defaults should be rewritten")
	}

	request.RequestedModel = "gpt-5.6"
	if call() {
		t.Fatal("non-devin model should not be rewritten")
	}

	request.RequestedModel = "devin/claude-code"
	raw, _ := json.Marshal(map[string][]byte{"config_yaml": []byte("enabled: false\n")})
	if _, err := handleMethod(pluginabi.MethodPluginReconfigure, raw); err != nil {
		t.Fatal(err)
	}
	if call() {
		t.Fatal("hot disable ignored")
	}

	raw, _ = json.Marshal(map[string][]byte{"config_yaml": []byte("enabled: true\nreplacements:\n  - from: hello\n    to: hi\n")})
	if _, err := handleMethod(pluginabi.MethodPluginReconfigure, raw); err != nil {
		t.Fatal(err)
	}
	if !call() {
		t.Fatal("custom replacements not applied after reconfigure")
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// 客户端原样发送 < > &，而 Go 编码器默认会转义成 < 等，两种形式都要能匹配。
func TestRewriteHTMLCharsBothForms(t *testing.T) {
	reps := []replacement{{From: "use <tool> & stuff", To: "ok"}}
	for _, body := range []string{
		`{"model":"devin/x","text":"use <tool> & stuff"}`,
		`{"model":"devin/x","text":"use <tool> & stuff"}`,
	} {
		out, n := rewrite([]byte(body), "devin/x", []string{"devin/*"}, reps)
		if n != 1 || string(out) != `{"model":"devin/x","text":"ok"}` {
			t.Fatalf("body %q -> n=%d out=%s", body, n, out)
		}
	}
}
