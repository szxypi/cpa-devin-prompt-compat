# 本机部署记录

2026-09-15 14:34:10（Asia/Shanghai）v0.1.0 热加载到 CPA 7.3.3；同日 v0.1.1（修复 `<>&` 转义不对称匹配，默认行为不变）经 `systemctl restart` 载入，日志 `plugin registered ... version=0.1.1`。

- 动态库：`/var/lib/cli-proxy-api/plugins/linux/amd64/cpa-devin-prompt-compat-v0.1.1.so`
- 权限：root:cliproxy，0550
- SHA-256：`70a9c272c73c705103380562c4fd9582491c15d67b417da8986275e897f56714`（v0.1.1；v0.1.0 为 eb6ea8c3…ff0b58）
- 配置：`plugins.configs.cpa-devin-prompt-compat`，enabled=true，priority=5，log-stats=true，models=["devin/*"]，replacements 用默认两条
- 配置为原地改写（inode 保留），日志 `pluginhost: plugin loaded / registered plugin_id=cpa-devin-prompt-compat version=0.1.1`

## 验证

用当天三条真实 403 请求体（164KB，Claude Code 子代理 system 提示词）原样回放本机 8317：

| 请求 | 部署前 | 部署后 |
| --- | --- | --- |
| 10798a8e | 403 content policy | 200，22.4s |
| 71add477 | 403 content policy | 200，59.8s |
| ee0e8392 | 403 content policy | 200，19.1s |

宿主日志：`[cpa-devin-prompt-compat] replaced=2 request_bytes=207553 result_bytes=207525 model=devin/swe-2`

## 回退

把配置中本插件的 `enabled` 原地改为 `false` 等热重载；不要 rename 覆盖 config.yaml。
