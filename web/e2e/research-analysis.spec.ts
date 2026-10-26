import { test, expect } from "@playwright/test";
import { createServer } from "node:http";

test("逐篇分析可展开、刷新保留并跳转原文引用", async ({ page, request }, testInfo) => {
  await request.put("/api/runtime/model", { data: { mode: "fixture", endpoint: "", model: "", api_key: "" } });
  const project = await (await request.post("/api/projects", { data: { title: "逐篇阅读验收" } })).json();
  await page.goto(`/p/${project.id}/research`);
  await page.getByRole("textbox", { name: "调研问题", exact: true }).fill("研究可复现的基线");
  await page.getByRole("button", { name: "开始调研", exact: true }).click();
  await page.getByRole("button", { name: "确认计划并继续", exact: true }).click();
  await page.getByRole("button", { name: "确认纳入并继续", exact: true }).click();
  await expect(page.locator('.research-task-status[data-state="completed"]')).toHaveText("已完成");
  await page.goto(`/p/${project.id}/research`);
  const analysis = page.getByRole("region", { name: "逐篇论文分析" });
  await expect(analysis.getByRole("status")).toContainText("已完成 1/1 篇");
  await analysis.locator("summary").click();
  await expect(analysis.getByText("有原文依据", { exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("paper-analysis.png"), fullPage: true });
  await page.reload();
  await analysis.locator("summary").click();
  await analysis.getByRole("link", { name: "查看原文引用 1", exact: true }).click();
  await expect(page.locator(".document-block-text mark")).toContainText("Fixture Research");
});

test("在途模型结束后暂停，刷新继续并单篇重试，累计预算保留", async ({ page, request }, testInfo) => {
  const project = await (await request.post("/api/projects", { data: { title: "暂停和失败恢复" } })).json();
  let calls = 0;
  let selected = "";
  let release!: () => void;
  let enter!: () => void;
  const entered = new Promise<void>((resolve) => { enter = resolve; });
  const waiting = new Promise<void>((resolve) => { release = resolve; });
  let releaseReport!: () => void;
  const reportGate = new Promise<void>((resolve) => { releaseReport = resolve; });
  const model = createServer(async (incoming, response) => {
    let body = "";
    for await (const chunk of incoming) body += chunk;
    const input = JSON.parse(body);
    calls++;
    const result = input.messages.at(-1);
    const raw = typeof result.content === "string" ? result.content : (result.content || []).map((item: { text?: string }) => item.text || "").join("");
    let call: { name: string; arguments: string } | null = null;
    const runs = await (await request.get(`/api/projects/${project.id}/research`)).json();
    const run = runs.items[0];
    if (!run.plan_revision) call = { name: "submit_research_result", arguments: JSON.stringify({ kind: "plan", expected_revision: 0, plan: { summary: "阅读并核对基线", queries: ["fixture"] } }) };
    else if (!run.approved_plan_revision) call = { name: "ask_user", arguments: JSON.stringify({ kind: "research_plan", expected_revision: run.plan_revision, question: "确认研究计划" }) };
    else if (!run.candidate_searches) call = { name: "search_papers", arguments: '{"text":"fixture","limit":1}' };
    else if (!run.approved_candidate_revision) call = { name: "ask_user", arguments: JSON.stringify({ kind: "research_candidates", expected_revision: run.candidate_revision, question: "确认候选论文", recommendations: run.candidates.map((c: { id: string }) => ({ candidate_id: c.id, recommended: true, reason: "相关基线" })) }) };
    else {
      const work = run.work_items[0];
      selected = work.paper_id;
      if (calls === 5) { enter(); await waiting; }
      if (work.state === "pending" || work.state === "reading") {
        if (!work.document_id) call = { name: "read_paper", arguments: JSON.stringify({ paper_id: selected, section: "abstract" }) };
        else if (!work.analysis_revision) call = { name: "submit_research_result", arguments: JSON.stringify({ kind: "paper_analysis", expected_revision: work.revision, paper_analysis: { paper_id: selected, document_id: work.document_id, status: "failed", error: "本地测试模拟实验资料缺失" } }) };
        else call = { name: "submit_research_result", arguments: JSON.stringify({ kind: "paper_analysis", expected_revision: work.revision, paper_analysis: { paper_id: selected, document_id: work.document_id, status: "completed", summary: "重试保留原文版本，摘要未核实的细节保持未知", fields: ["method", "datasets", "metrics", "code", "reproduction"].map((name) => ({ name, status: "unknown", text: "摘要未核实" })) } }) };
      } else {
        const matrix = await (await request.get(`/api/projects/${project.id}/research/${run.id}/matrix`)).json();
        if (!run.latest_report || run.latest_report.matrix_hash !== matrix.hash) {
          if (run.latest_report) await reportGate;
          call = { name: "submit_research_result", arguments: JSON.stringify({ kind: "report", expected_revision: run.latest_report?.revision || 0, report: {
            title: "本地协议测试报告", summary: "仅验证报告版本和恢复流程。", matrix_hash: matrix.hash,
            limitations: ["本地脚本模型；仅摘要，未经实验核实。"],
            claims: ["comparison", "conflicts", "gaps", "next_steps"].map((section) => ({ section, status: "unknown", text: "尚需核实原文和实验。", analysis_refs: [{ work_item_id: work.id, analysis_revision: work.analysis_revision }] })),
          } }) };
        }
      }
    }
    const delta = call ? { role: "assistant", tool_calls: [{ index: 0, id: `analysis-${calls}`, type: "function", function: call }] } : { role: "assistant", content: "已保存本轮逐篇结果。" };
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\ndata: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: call ? "tool_calls" : "stop" }] })}\n\ndata: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 12, completion_tokens: 3, total_tokens: 15 } })}\n\ndata: [DONE]\n\n`);
  });
  await new Promise<void>((resolve) => model.listen(0, "127.0.0.1", resolve));
  try {
    const address = model.address();
    if (!address || typeof address === "string") throw new Error("本地模型未启动");
    await request.put("/api/runtime/model", { data: { mode: "remote", endpoint: `http://127.0.0.1:${address.port}/v1`, model: "analysis-test", api_key: "test-only" } });
    await page.goto(`/p/${project.id}/research`);
    await page.getByRole("textbox", { name: "调研问题", exact: true }).fill("研究基线");
    await page.getByRole("button", { name: "开始调研", exact: true }).click();
    await page.getByRole("button", { name: "确认计划并继续", exact: true }).click();
    await page.getByRole("button", { name: "确认纳入并继续", exact: true }).click();
    await entered;
    await page.goto(`/p/${project.id}/research`);
    const analysis = page.getByRole("region", { name: "逐篇论文分析" });
    await analysis.getByRole("button", { name: "暂停调研", exact: true }).click();
    await expect(analysis.getByText("等待安全边界暂停", { exact: true })).toBeVisible();
    release();
    await expect(analysis.getByRole("button", { name: "继续调研", exact: true })).toBeVisible();
    await page.reload();
    await analysis.getByRole("button", { name: "继续调研", exact: true }).click();
    await expect(analysis.getByRole("status")).toContainText("失败 1 篇");
    await analysis.locator("summary").click();
    await expect(analysis.getByText("失败原因：本地测试模拟实验资料缺失", { exact: true })).toBeVisible();
    const before = (await (await request.get(`/api/projects/${project.id}/research`)).json()).items[0];
    await analysis.getByRole("button", { name: "重试此论文", exact: true }).click();
    await expect(analysis.getByRole("status")).toContainText("已完成 1/1 篇");
    const after = (await (await request.get(`/api/projects/${project.id}/research`)).json()).items[0];
    expect(after.id).toBe(before.id);
    expect(after.model_calls).toBeGreaterThan(before.model_calls);
    expect(after.work_items[0].document_id).toBe(before.work_items[0].document_id);
    expect(after.work_items[0].analysis_revision).toBe(2);
    expect(after.work_items[0].attempts).toBe(2);
    expect(after.work_items[0].attempt_id).not.toBe(before.work_items[0].attempt_id);
    await page.goto(`/p/${project.id}/research/${after.id}`);
    const reportPanel = page.getByRole("region", { name: "证据矩阵与研究报告" });
    await expect(reportPanel).toContainText("此报告属于历史矩阵版本");
    releaseReport();
    await expect(reportPanel.getByRole("combobox", { name: "报告版本" })).toContainText("v2");
    const versions = await (await request.get(`/api/projects/${project.id}/research/${after.id}/reports`)).json();
    expect(versions.items).toHaveLength(2);
    await reportPanel.getByRole("combobox", { name: "报告版本" }).selectOption(versions.items[0].id);
    await expect(reportPanel).not.toContainText("此报告属于历史矩阵版本");
    await expect(reportPanel).toContainText("不可变报告 v2");
    await reportPanel.getByRole("combobox", { name: "报告版本" }).selectOption(versions.items[1].id);
    await expect(reportPanel).toContainText("此报告属于历史矩阵版本");
    await page.screenshot({ path: testInfo.outputPath("analysis-retry.png"), fullPage: true });
  } finally {
    releaseReport();
    release();
    await request.put("/api/runtime/model", { data: { mode: "fixture", endpoint: "", model: "", api_key: "" } });
    model.closeAllConnections();
    await new Promise<void>((resolve) => model.close(() => resolve()));
  }
});
