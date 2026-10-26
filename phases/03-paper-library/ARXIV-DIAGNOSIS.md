# arXiv 搜索失败诊断

日期：2026-10-23。范围：历史失败证据、固定版本 `arxiv-mcp-server==0.8.2` 的错误处理和真实 MCP 请求；不修改 goHarness 框架。

## 当前结论

历史错误定位到 MCP 子进程访问 `https://export.arxiv.org/api/query` 的 HTTP 环节。第三方包曾报告捕获 HTTP 429；当前相同查询已恢复，新增领域查询也成功。尚不能确定当时 429 的具体触发策略，不能把它直接归因为 VPN、整个 IP 被封或本项目请求过频。

进一步确认了诊断信息损失：第三方包在“先收到 429、下一次请求耗尽总时间预算”的情况下，最终仍返回 `ArxivRateLimitError`、HTTP 429 和“等待 60 秒”；最后一次超时没有体现在最终消息中。60 秒可以是包内默认值，不代表响应头里的 `Retry-After`。已用离线实验复现这一行为，但不能把模拟序列认定为历史请求的实际顺序。

## 历史证据

从本任务原始执行记录提取，以下是结果记录时间（UTC），不是每次 HTTP 的开始时间：

| 时间 | 记录 |
| --- | --- |
| 05:03:09 | 初次 Graph 联调约 30.13 秒失败，外层只显示工具失败；未保存原始 HTTP 响应。 |
| 05:05:24 | 联调约 51.71 秒失败，显示 `status=rate_limited`，消息包含 `HTTP 429` 和等待 60 秒。 |
| 05:10:33 | 联调约 52.28 秒失败，只显示 MCP 工具错误；当时没有展开工具错误正文。 |
| 05:11:45 | 按编号获取、Graph 归档和摘要读取通过，约 1.93 秒。 |

既有后端日志没有找到能补足本次故障的 HTTP 响应头、错误正文和逐次重试明细。应用的错误正文提取已在此前改进，但无法补回历史数据。51.71 秒的最终文案不足以证明整个等待期间持续返回 429。

## 带 HTTP 取证的真实复测

通过临时包装器启动真实 MCP 0.8.2 子进程，在 HTTPX 请求边界记录时间、URL、状态、白名单响应头、目标地址和 TLS 信息，同时读取 MCP stderr。没有调用模型或用户数据库。

诊断子进程关闭自动重试，每次调用间隔超过 3 秒，遇到错误即停止。2026-10-23 08:05 UTC 的结果：

| 请求 | HTTP | HTTP 耗时 | 业务结果 |
| --- | --- | --- | --- |
| 编号 `1706.03762` | 200 | 0.720 秒 | 获取指定论文。 |
| `ti:"Attention Is All You Need"` | 200 | 0.679 秒 | 首条为指定论文。 |
| 普通标题关键词 | 200 | 0.677 秒 | 692151 个匹配，首条为另一篇相关论文。 |
| 时间序列预测 + cs.LG + 日期范围 | 200 | 0.897 秒 | 1320 个匹配，返回 3 篇候选。 |

最后一条响应为 `X-Cache: MISS, MISS, MISS`、`Age: 0`，说明本次成功覆盖了未命中缓存的请求。响应经过 arXiv 的 CDN/Google Frontend，TLS 证书校验开启，证书颁发方为 Certainly。此结果说明当前访问正常，不证明历史 429 由哪台边缘节点或源站产生。

原始证据保存在应用目录：

- `.cache/diagnostics/arxiv-20261023-first-capture/http-trace.jsonl`
- `.cache/diagnostics/arxiv-20261023-first-capture/results.json`
- `.cache/diagnostics/arxiv-20261023-first-capture/mcp-stderr.log`

之后又通过正式项目脚本执行一轮相同四项检查，均返回 HTTP 200，记录位于 `.cache/diagnostics/arxiv-_qczpnxb/`。再执行实际 goHarness Graph 联调：

```powershell
$env:AUTORESEARCH_ARXIV_LIVE = '1'
$env:AUTORESEARCH_ARXIV_KEYWORD_LIVE = '1'
go test ./internal/app -run '^TestArxivLiveKeywordGraph$' -v -count=1
```

结果通过，约 5.83 秒，真实完成搜索、归档和摘要冻结。未发现本次问题由 Graph 编排或 MCP 协议转换产生的证据。

## 离线错误序列复现

直接调用已安装包的 `retry_with_backoff`，用模拟 HTTP 响应和短时间预算复现，不访问网络，也不修改第三方包：

| 模拟输入序列 | 包最终报告 |
| --- | --- |
| HTTP 429，然后下一次请求超过剩余预算 | `ArxivRateLimitError`，HTTP 429，等待 60 秒。 |
| HTTP 503，然后下一次请求超过剩余预算 | `ArxivRateLimitError`，HTTP 503，等待 60 秒。 |
| 只有请求超时，没有收到 HTTP 错误状态 | `ArxivTimeoutError`。 |

因此，当前这条包内路径没有凭空把普通超时变成 HTTP 429；但会把此前 429 与后续超时合并成一条限流消息。实际首次 429 的成因仍需对应原始响应或 arXiv 服务端记录才能进一步确认。离线复现脚本保存在 `.cache/diagnostics/arxiv-20261023-first-capture/offline-error-replay.py`。

## 网络与上游资料核对

- 当前没有检测到环境代理、Windows 系统代理或常见活动 VPN 虚拟网卡；禁用环境代理的直接请求也成功。这不能还原历史 VPN 状态，也不能排除路由层代理。
- 故障时间窗内，没有查到可用于证明连接切换的 Windows NetworkProfile 事件；没有记录不等于没有切换。
- [arXiv 官方接口说明](https://info.arxiv.org/help/api/tou.html)要求串行访问、至少每 3 秒一次；该限制是所有受控机器合计，不只针对单个进程。第三方包的门限仅在单个 MCP 进程内共享，多进程或共享出口仍可能累积流量，但本次没有证据证明这就是历史故障的触发因素。
- 第三方项目的 [#238](https://github.com/blazickjp/arxiv-mcp-server/issues/238)记录过相同 429 消息，[#277](https://github.com/blazickjp/arxiv-mcp-server/issues/277)记录过无代理时的间歇性边缘限流。这些报告证明现象已有先例，不能替代本机历史响应证据。

## 后续故障取证方式

从应用根目录执行：

```powershell
uv run --no-project --with arxiv-mcp-server==0.8.2 python scripts/diagnose_arxiv.py
```

已经缓存依赖时，可增加 `--offline`，避免诊断前访问包索引。脚本只访问四个固定论文样例；不会调用 LLM、修改系统代理或触碰现有数据库。输出在 `.cache/diagnostics/arxiv-*/`，HTTP 错误会保留响应头及最多 1000 字符正文；工具返回错误时以非零状态结束。

该脚本是显式运行的独立诊断工具，普通后端请求尚未自动开启 HTTP 取证。若以后再次发生失败，应在故障窗口内运行它，才能区分原始 429、503、406、连接失败与重试超时。当前不需要通过高频请求人为制造限流；这种做法既可能延长限制，也不能证明历史故障原因。
