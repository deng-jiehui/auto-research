import { describe, expect, it } from "vitest";
import { assistantBlocks, messageText, noteSummary, parsePendingQuestion, parseResearchProgress, parseWorkspaceToolEvent, shouldSubscribeToEvents, streamingAssistantText, turnFailureHint, turnFailureText, visibleMessages, visibleProgress, visibleWorkspaceTools } from "./SessionPage";

describe("会话消息解析", () => {

  it("切换报告后历史消息仍显示原来的报告版本，不合并不同引用的问题", () => {
    const record = (id: string, revision: number) => ({ id, committed: true, content_json: JSON.stringify({
      role: "user", content_blocks: [{ type: "text", text: { text: "这个结论依据是什么？" } }],
      extra: { report_context: { run_id: "run-1", report_id: id, revision, title: "比较报告" } },
    }) });
    const messages = visibleMessages([record("report-v1", 1), record("report-v2", 2)]);
    expect(messages).toHaveLength(2);
    expect(messages[0]).toMatchObject({ report: { report_id: "report-v1", revision: 1 } });
    expect(messages[1]).toMatchObject({ report: { report_id: "report-v2", revision: 2 } });
  });

  it("候选审核保留结构化候选和审核身份，不退化成普通自由文本回答", () => {
    const prompt = { kind: "research_candidates", question: "选择论文", run_id: "run-1", revision: 4, hash: "candidate-hash", analyze_limit: 2, candidates: [{ id: "candidate-1", source_key: "arxiv:2401.00001", title: "论文", recommended: true, recommendation_reason: "相关基线" }] };
    const result = parsePendingQuestion(JSON.stringify([{ id: "interrupt-1", payload: prompt }]));
    expect(result?.researchCandidates).toEqual(prompt);
    expect(result?.researchPlan).toBeUndefined();
  });

  it("检索卡片使用完整论文元数据，失败工具不提供加入按钮数据", () => {
    const papers = [{ source_key: "arxiv:2401.00001", title: "真实工具结果", abstract: "完整摘要".repeat(300) }];
    const event = { kind: "academic.tool", payload_json: JSON.stringify({ tool: "search_papers", call_id: "search-full", status: "completed", is_error: false, preview: "截断的预览", papers }) };
    expect(parseWorkspaceToolEvent(event)).toMatchObject({ papers });
    expect(parseWorkspaceToolEvent({ ...event, payload_json: JSON.stringify({ tool: "search_papers", call_id: "failed", status: "failed", is_error: true, papers }) })).toMatchObject({ papers: [] });
  });
  it("解析中断问题的快捷选项并保留自定义输入配置", () => {
    expect(parsePendingQuestion(JSON.stringify([
      {
        id: "scope-1",
        payload: JSON.stringify({
          question: "选择研究对象",
          choices: ["仓库级缺陷", "单文件缺陷"],
          allow_custom: true,
          placeholder: "也可以输入自己的范围",
        }),
      },
    ]))).toEqual({
      id: "scope-1",
      question: "选择研究对象",
      choices: ["仓库级缺陷", "单文件缺陷"],
      allowCustom: true,
      placeholder: "也可以输入自己的范围",
    });
  });

  it("可以读取后端 []byte JSON 的 Base64 表示", () => {
    const content = JSON.stringify({
      role: "user",
      content_blocks: [{ type: "text", text: { text: "测试消息" } }],
    });
    const encoded = Buffer.from(content, "utf8").toString("base64");
    expect(messageText({ id: "1", content_json: encoded, committed: true })).toEqual({
      role: "user",
      text: "测试消息",
    });
  });

  it("失败轮次展示后端记录的具体错误", () => {
    expect(turnFailureText({ state: "failed", error_message: "模型不存在" } as never)).toBe("模型不存在");
    expect(turnFailureText({ state: "failed" } as never)).toContain("执行失败");
  });

  it("模型响应超时显示针对性的重试提示", () => {
    expect(turnFailureHint({ state: "failed", error_message: "context deadline exceeded" } as never)).toContain("响应超时");
  });

  it("项目笔记过长显示针对性的提示", () => {
    expect(turnFailureHint({ state: "failed", error_message: "项目笔记超过 4000 个字符" } as never)).toContain("笔记过长");
  });

  it("连续重发的相同用户消息只展示一次", () => {
    const record = (id: string, role: "user" | "assistant", text: string) => ({
      id,
      turn_id: `turn-${id}`,
      content_json: JSON.stringify({ role, content_blocks: [{ type: "text", text: { text } }] }),
      committed: true,
    });
    const messages = visibleMessages([
      record("1", "user", "同一个研究问题"),
      record("2", "user", "同一个研究问题"),
      record("3", "assistant", "第一次回答"),
      record("4", "user", "同一个研究问题"),
    ]);
    expect(messages.map((item) => item.text)).toEqual(["同一个研究问题", "第一次回答", "同一个研究问题"]);
  });

  it("解析并按稳定 id 合并研究进度事件", () => {
    const event = {
      id: 10,
      kind: "research.progress",
      payload_json: JSON.stringify({ id: "turn-1:artifact", stage: "artifact", status: "completed", title: "阶段产物", summary: "已形成关注点" }),
    };
    expect(parseResearchProgress(event)).toMatchObject({ id: "turn-1:artifact", title: "阶段产物" });
    expect(visibleProgress([
      event,
      { ...event, id: 11, payload_json: JSON.stringify({ id: "turn-1:artifact", stage: "artifact", status: "updated", title: "阶段产物", summary: "已更新关注点" }) },
    ]).map((item) => item.summary)).toEqual(["已更新关注点"]);
  });

  it("累积答复增量，完成消息出现后由正式消息接管", () => {
    const base = { id: 1, kind: "assistant.delta", payload_json: "" };
    expect(streamingAssistantText([
      { ...base, payload_json: JSON.stringify({ message_id: "m", delta: { message_id: "m", op: "block_append", text: "先看" } }) },
      { ...base, id: 2, payload_json: JSON.stringify({ message_id: "m", delta: { message_id: "m", op: "block_append", text: "评测对象" } }) },
    ])).toBe("先看评测对象");
  });

  it("把助手 Markdown 答复拆成标题、列表和段落", () => {
    expect(assistantBlocks("# 研究方向\n\n- 评测对象\n- 基线设计\n\n下一步先建立可重复的修复基线。"))
      .toEqual([
        { kind: "heading", text: "研究方向" },
        { kind: "unordered", items: ["评测对象", "基线设计"] },
        { kind: "paragraph", text: "下一步先建立可重复的修复基线。" },
      ]);
  });

  it("只有活动轮才订阅 SSE，发送新轮后可以重新建立连接", () => {
    expect(shouldSubscribeToEvents("running")).toBe(true);
    expect(shouldSubscribeToEvents("awaiting_input")).toBe(true);
    expect(shouldSubscribeToEvents("completed")).toBe(false);
  });

  it("项目笔记折叠预览只显示短摘要", () => {
    expect(noteSummary("# 研究目标\n\n这是一段完整笔记正文，打开后再查看。", 40)).toBe("研究目标");
  });

  it("解析工作区工具事件并按调用 id 去重", () => {
	const academic = { kind: "academic.tool", payload_json: JSON.stringify({ tool: "search_papers", call_id: "paper-1", status: "completed", is_error: false }) };
	expect(parseWorkspaceToolEvent(academic)).toMatchObject({ tool: "search_papers" });
    const event = { id: 20, kind: "workspace.tool", payload_json: JSON.stringify({ tool: "read_file", call_id: "call-1", status: "completed", is_error: false, preview: "README.md" }) };
    expect(parseWorkspaceToolEvent(event)).toMatchObject({ tool: "read_file", status: "completed" });
    expect(visibleWorkspaceTools([
      event,
      { ...event, id: 21, payload_json: JSON.stringify({ tool: "read_file", call_id: "call-1", status: "completed", is_error: false, preview: "README updated" }) },
    ])).toMatchObject([{ call_id: "call-1", preview: "README updated" }]);
  });
});
