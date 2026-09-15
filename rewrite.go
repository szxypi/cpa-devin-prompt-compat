package main

import (
	"bytes"
	"encoding/json"
	"path"
)

// replacement 表示对原始请求体做的一条字面文本替换规则。
type replacement struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

const maxRequestBytes = 32 << 20

// matchModel 判断 model 是否命中配置的任一通配符模式。
// 模式按 path.Match 语义匹配（区分大小写，"*" 为通配符）。
func matchModel(model string, patterns []string) bool {
	if model == "" {
		return false
	}
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if ok, err := path.Match(p, model); err == nil && ok {
			return true
		}
	}
	return false
}

// jsonLiterals 返回 s 出现在 JSON 字符串值内部时可能的转义字面量（去掉首尾引号）。
// 不同客户端对 < > & 的处理不一致：Node/Python 原样输出，Go 默认转成 \u003c 等，
// 因此同时给出"不转义 HTML"与"转义 HTML"两种形式（相同时只返回一种），
// 保证对 JSON 请求体做字节级替换后仍合法。
func jsonLiterals(s string) [][]byte {
	encode := func(escapeHTML bool) []byte {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(escapeHTML)
		if err := enc.Encode(s); err != nil {
			return nil
		}
		b := bytes.TrimRight(buf.Bytes(), "\n")
		if len(b) >= 2 {
			b = b[1 : len(b)-1]
		}
		return b
	}
	plain := encode(false)
	escaped := encode(true)
	if bytes.Equal(plain, escaped) {
		return [][]byte{plain}
	}
	return [][]byte{plain, escaped}
}

// rewrite 对 body 应用所有配置的替换规则，采用 JSON 转义字面量匹配，
// 保证输入为 JSON 时输出仍是合法 JSON。返回改写后的字节内容（未命中时
// 原样返回）以及所有规则命中的总替换次数。
func rewrite(body []byte, model string, models []string, replacements []replacement) ([]byte, int) {
	if len(body) == 0 || len(body) > maxRequestBytes || !matchModel(model, models) {
		return body, 0
	}
	out := body
	total := 0
	for _, r := range replacements {
		if r.From == "" {
			continue
		}
		to := jsonLiterals(r.To)[0]
		for _, from := range jsonLiterals(r.From) {
			count := bytes.Count(out, from)
			if count == 0 {
				continue
			}
			out = bytes.ReplaceAll(out, from, to)
			total += count
		}
	}
	if total == 0 {
		return body, 0
	}
	return out, total
}
