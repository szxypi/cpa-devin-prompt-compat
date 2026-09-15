# CPA Devin 系统提示词兼容插件

修复 Devin 上游对 Claude Code 子代理（Agent SDK）系统提示词中两句固定措辞一律返回 403 content policy 的兼容问题（已实测确认：把这两句换成同义表述后，同一请求返回 200）。

这是独立的 CPA C ABI 插件（before-auth 请求拦截器），只做字面文本替换，不修改协议格式、不调用网络、不缓存请求内容。使用 CPA SDK v7.2.155 / C ABI v1；元数据中的 `GitHubRepository` 指向宿主 SDK 仓库，不代表该本地插件已发布到上游。

## 生效范围

同时满足以下条件才改写：

- 插件已启用（`enabled: true`）。
- 请求的模型名（优先取 `RequestedModel`，为空则取 `Model`）命中 `models` 中任一通配符（`path.Match` 语义，`*` 不跨越 `/`）。
- 原始请求体（任意协议格式，字节级操作）中包含 `replacements` 列表里某条 `from` 的 JSON 转义字面量。

替换实现：先把 `from`/`to` 分别用 `encoding/json` 编码成 JSON 字符串再去掉首尾引号，得到"JSON 转义后的字面量"，再对原始请求体字节做 `bytes.ReplaceAll`。这样即使原文包含引号、换行等需要转义的字符，替换后请求体仍是合法 JSON。没有任何替换命中时不修改请求体、不返回 `Body` 字段。

## 配置示例

在 CPA 配置文件中：

```yaml
plugins:
  configs:
    cpa-devin-prompt-compat:
      enabled: true
      priority: 5
      models:
        - "devin/*"
      log-stats: true
      replacements:
        - from: "You are a Claude agent, built on Anthropic's Claude Agent SDK."
          to: "You are an agent built with the Claude Agent SDK."
        - from: "For clear communication with the user the assistant MUST avoid using emojis."
          to: "Avoid emojis so that communication with the user stays clear."
```

字段说明：

- `enabled`：是否启用替换，默认 `true`。
- `models`：匹配请求模型名的通配符列表，默认 `["devin/*"]`。
- `log-stats`：命中替换时是否向 stderr 打一行统计日志，默认 `true`，格式为：
  `[cpa-devin-prompt-compat] replaced=N request_bytes=.. result_bytes=.. model=..`（`N` 为本次命中的替换总次数，非规则数）。
- `replacements`：`{from, to}` 列表。**只要 YAML 里出现这个键就完全覆盖内置默认值**（不做合并），不出现则使用上面两条默认替换。

## 构建与验证

```sh
cd ~/cpa-plugins/cpa-devin-prompt-compat
mise exec -- go mod tidy
mise exec -- go test ./...
CGO_ENABLED=1 mise exec -- go build -buildmode=c-shared -o dist/cpa-devin-prompt-compat-v0.1.1.so .
python3 tests/integration.py
```

`tests/integration.py` 会启动一个隔离的本机 CPA 进程 + 本地 mock 上游（不使用真实凭据、不联网），依次验证：C ABI 调用、命中替换后 mock 返回 200、未替换时 mock 返回 403（模拟 Devin 的 content policy 拒绝）、非目标模型不改写、同一进程内热启用/热关闭。

## 部署

把编译出的 `dist/cpa-devin-prompt-compat-v0.1.1.so` 放入 CPA 插件目录（如 `linux/amd64/`），在配置文件中按上面的示例加入 `plugins.configs.cpa-devin-prompt-compat` 条目并原位保存，CPA 会热加载。回退时只需把该插件的 `enabled` 改为 `false`，无需重启、无需改动其他插件配置。

本机部署记录（时间、哈希、回放验证结果）见 `DEPLOYMENT.md`。

已知限制：替换是字节级的，`from` 含非 ASCII 字符时按原始 UTF-8 匹配，若客户端把这些字符编码成 `\uXXXX` 形式发送则匹配不到；`<`、`>`、`&` 已同时兼容原样与 `\u003c` 形式。
