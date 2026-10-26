# 阶段 03 验收：论文库、真实 MCP 与 Graph 工具

更新日期：2026-10-25。

2026-10-25 已补齐全文、项目检索和证据定位，最新记录在文末；前面保留 10-23 的历史接入与诊断证据。

本轮已完成两项代码接入：真实 arXiv MCP 配置/连接，以及现有 Agent Graph 中的论文检索、归档、列出和摘要读取。真实外部联调中，按编号获取论文、关键词检索与 Graph 归档/读取均已成功；此前 HTTP 429 的诊断和复测结果见下文。

## 已交付

- `config.arxiv.example.json` 启用真实来源，固定 `arxiv-mcp-server==0.8.2`；启动时通过框架 MCP 扩展完成握手和工具发现。
- 远端白名单为 `search_papers`、`get_abstract`；本地模型工具为 `search_papers`、`save_paper`、`list_papers`、`read_paper`。
- 模型结构化选择 academic 路线，再自主选择工具；信息不足时使用既有 ask_user 中断，恢复后继续原工具上下文。
- 应用注入可信 Scope；重复归档及来源文本按身份/内容去重，不覆盖已有用户笔记。
- 前端显示来源模式、搜索空结果和论文工具过程，论文库可展开冻结摘要；刷新后保留本轮全部业务事件。
- 超时、MCP isError、错误业务状态和无效结果明确报错；来源限流后进入冷却，不切换为演示数据。
- 图定义和 StateCodec 升级到第三版；旧未完成检查点使用原有 recovery_required 路径处理。

全部代码变更位于 AutoResearch 项目，没有修改 goHarness 框架。

## 自动化验证

在项目根目录执行：

```powershell
go test -count=1 ./...
go vet ./...
npm --prefix web run typecheck
npm --prefix web test
npm --prefix web run build
npm --prefix web run test:e2e
```

实际结果：Go 全量测试和 vet 通过；TypeScript 检查、构建和 22 项前端单元测试通过；15 项浏览器测试通过。Vite 保留依赖中的既有 use client 提示，不影响构建。

新增回归覆盖空搜索、MCP isError、structuredContent 优先、真实服务 schema/日期参数、元数据解析、摘要缓存、限流冷却、项目隔离，以及“搜索→归档→ask_user→恢复→读取→保存项目笔记”。关键缺口先运行失败用例，再实现并验证通过。

两条新增浏览器测试覆盖会话中的搜索/归档/摘要读取/刷新，以及论文库的搜索/显式归档/空结果。测试使用独立临时数据库和离线来源，证明应用流程，不作为真实 arXiv 搜索成功的证据。

2026-10-23 修复论文答复二次改写：现场记录显示搜索工具成功，论文节点草稿正确，但通用整理模型随后替换论文清单并错误声称不能联网。论文路线现直接发布工具循环的最终答复，减少一次模型调用。`TestAcademicGraphPreservesToolBasedReply` 覆盖成功、空结果、来源不可用，以及长答复保存压缩笔记四种情况，同时检查公共输出快照。修复前回归失败；修复后四项、应用全量 Go 测试和 `go vet` 均通过，未使用真实模型 Token。

## 真实 MCP 联调

本机已安装 uv/uvx，服务固定为 0.8.2。stdio 启动命令等价于：

```powershell
uvx --from arxiv-mcp-server==0.8.2 arxiv-mcp-server --storage-path <应用数据目录>/arxiv-mcp
```

框架 MCP Client 执行 initialize/tools/list 并确认两个白名单工具存在，再调用 get_abstract(paper_id="1706.03762")。

实际结果：

- 标题 Attention Is All You Need，年份 2017，完整摘要 1136 字符。
- 通过真实来源、真实 Graph 和临时 SQLite 执行 save_paper→read_paper，论文与冻结文本保存成功。
- source_key：`arxiv:1706.03762`。
- locator：`arxiv:1706.03762:abstract`。
- SHA-256：`062c384a940b00416e76c513973d595aeb1fbc6e0e09a433c18fddc5bdf529eb`。
- 工具选择由脚本模型固定，没有使用用户 API Key 或消耗模型 Token；不等同于真实模型回答质量验收。

复测成功路径：

```powershell
$env:AUTORESEARCH_ARXIV_LIVE = '1'
go test ./internal/app -run '^TestArxivLiveGraph$' -v -count=1
```

首次真实关键词联调中，上游返回过 rate_limited / HTTP 429 和重试建议，后续请求也遇到 MCP 错误；没有把这些结果当作空搜索或成功记录。2026-10-23 继续诊断并复测：

- 绕过应用、Graph 和 MCP 传输，用第三方包相同的 URL、User-Agent 和 HTTP 客户端直接单次请求 arXiv；编号查询返回 HTTP 200（约 0.73 秒），原普通标题关键词查询返回 HTTP 200（约 0.48 秒）。请求串行，间隔超过 3 秒，没有自动重试。
- 第三方包仅在捕获相应 HTTP 状态后生成包含该状态码的限流提示；它也将 503/406 归为限流。上次文案中的 HTTP 429 不是 goHarness 生成的解析错误；具体限流触发条件缺少当时的响应头和上游服务日志，不能断言是本项目请求过频或整个 IP 被封。提示中的等待 60 秒可能来自包内默认值。
- 第三方包默认最多重试 2 次、总预算 50 秒。进一步离线复现证实：先收到 429、随后请求耗尽剩余预算时，最终仍返回同一条限流提示；此前较长等待不能证明全程持续收到 429。完整历史证据、HTTP 取证和诊断脚本见 [arXiv 搜索失败诊断](ARXIV-DIAGNOSIS.md)。
- 另发现独立的验收问题：普通文本会被展开为标题/摘要中的宽泛词项查询，`Attention Is All You Need` 当次返回 692151 个匹配，第一条是另一篇相关论文。不能据此断言指定论文必须排第一；此问题与历史 HTTP 429 的触发原因分开记录。
- 验收请求改用 `ti:"Attention Is All You Need"` 后，`TestArxivLiveKeywordGraph` 在约 4.83 秒内通过，实际执行真实 MCP 搜索→Graph 归档→读取摘要→临时 SQLite 冻结文本。验证的 source_key、locator、SHA-256 与上述指定论文一致。仅修改应用验收查询，没有修改框架或第三方包。

关键词全链路复测命令：

```powershell
$env:AUTORESEARCH_ARXIV_LIVE = '1'
$env:AUTORESEARCH_ARXIV_KEYWORD_LIVE = '1'
go test ./internal/app -run '^TestArxivLiveKeywordGraph$' -v -count=1
```

两个网络测试默认跳过；外部失败会保留真实错误，不以 fixture 替代。

## 手动体验

在项目根目录启动后端，再在另一个终端启动前端：

```powershell
go run . serve --config config.arxiv.example.json
```

```powershell
npm --prefix web run dev
```

前端左下角配置真实模型后，新建会话发送：

> 将 arXiv:1706.03762 归档到本项目论文库，读取摘要，并说明论文的主要研究问题。明确标注你的阅读范围。

检查工具过程、最终回答和论文库新增的冻结摘要。也可以发送：

> 搜索 2021 到 2026 年关于 time series forecasting 的 3 篇 arXiv 论文，先展示候选，暂不归档。

## 2026-10-23 历史交付边界

- 读取范围为摘要；全文下载和 PDF 解析尚未接入，section=full 明确拒绝。
- 文本存入 SQLite；Artifact Store、大文件外置、向量检索和自动证据抽取不在本轮交付范围。
- 具体 arXiv 历史版本号尚未单独持久化；冻结文本使用内容哈希核验。
- 真实关键词端到端链路已通过本次复测；外部服务仍可能临时限流或不可用，历史 HTTP 429 的具体触发策略尚未确定。
- 已完成本轮代码接入与真实连接联调；不代表整个阶段三的所有增强都已完成。

## 2026-10-25：全文、项目检索与证据闭环

已交付 arXiv HTML 获取、404/410 时的 PDF 回退、原始文件与冻结文本 Artifact、分块目录、FTS5 英文词项及中文短语检索、唯一 Quote 定位和 Evidence 深链接。长正文不随文档列表返回；普通列表仍保留已有小摘要的兼容形状。所有新能力留在 AutoResearch。

论文页“获取全文”返回目录，逐块读取；“引用本段”可编辑引文/字段/解读，保存时验证唯一逐字匹配。点击“查看原文”打开冻结版本并高亮，刷新仍可定位。论文项目笔记默认折叠，文案区分个人整理和原文证据。

失败获取记录状态与原因，摘要保留；重启时残留 running 转成可重试失败。既有文档补块索引，正文及旧证据不改写。新版 single/multi 支持全文与证据；single-v1/multi-v1 保留原摘要 schema，v2 保留全文工具，v3 增加资料视图/方向。

实际验证：

- 本模块 Go 全量测试及 go vet 通过；前端 23 项单测、类型检查与生产构建通过。
- documents.spec.ts 覆盖手动获取→逐块阅读→保存引用→刷新定位，以及同一脚本模型调用 read_paper→read_paper(block)→search_project_evidence→save_evidence→在会话点击引用；未自动增加项目笔记。
- 真实 arXiv HTML：2303.08774v6，98697 个码点、635455 原始字节；1706.03762v7，34742 个码点、188707 原始字节。
- 真实 PDF：1706.03762v7，2215244 字节，Poppler 提取 59272 个码点和 15 页；两页小 PDF fixture 验证真实分页。
- Poppler pdftotext 26.09.0，下载到本任务忽略的 .cache/poppler/package。测试进程 PATH 加入其 Library/bin 和现有运行库目录；没有修改系统 PATH 或安装全局软件。
- HTML 空正文、超限、非法来源、跨域重定向、429 不回退、缺少解析器明确失败；另覆盖跨用户/项目、并发 Artifact 复用、损坏哈希、Unicode、重复 Quote、新文档保留旧引用、失败刷新后复用成功文档。

公开全文与 PDF 验证命令（解析器和运行库需已配置）：

```powershell
$env:AUTORESEARCH_DOCUMENT_LIVE = '1'
$env:AUTORESEARCH_PDFTOTEXT = '<pdftotext.exe 的绝对路径>'
go test ./internal/infra/documents -run 'TestArxivFullTextLive|TestArxivPDFLive|TestPDFExtractsActualPageBoundaries' -v -count=1
```

网络测试默认跳过。公开下载和本地脚本模型测试不使用用户密钥或收费模型；验证了工具与数据链路，不等于真实模型的论文理解质量。HTML/PDF 仍是文本提取，复杂公式、表格、扫描件 OCR 和语义检索有待后续验证；当前不做 OCR 或向量检索。
