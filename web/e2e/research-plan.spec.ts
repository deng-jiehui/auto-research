import { test, expect } from "@playwright/test";
import { createServer } from "node:http";

function pendingJSON(raw: string) {
  try { return JSON.parse(raw); }
  catch { return JSON.parse(Buffer.from(raw, "base64").toString("utf8")); }
}

test("全部排除后重新检索，旧审核表单返回 409，确认请求重发只归档一次", async ({ page, request }, testInfo) => {
  await request.put("/api/runtime/model", { data: { mode: "fixture", endpoint: "", model: "", api_key: "" } });
  const project = await (await request.post("/api/projects", { data: { title: "候选重检与幂等" } })).json();
  await page.goto(`/p/${project.id}/research`);
  await page.getByRole("textbox", { name: "调研问题", exact: true }).fill("检索可复现的基线");
  await page.getByRole("button", { name: "开始调研", exact: true }).click();
  await page.getByRole("button", { name: "确认计划并继续", exact: true }).click();
  const review = page.getByRole("region", { name: "候选论文审核" });
  await expect(review).toBeVisible();
  const runID = page.url().split("/research/")[1];
  const run = await (await request.get(`/api/projects/${project.id}/research/${runID}`)).json();
  const sessionID = run.session_id;
  const sessionURL = `/api/projects/${project.id}/sessions/${sessionID}`;
  const oldSnapshot = await (await request.get(`${sessionURL}/snapshot`)).json();
  const oldPending = pendingJSON(oldSnapshot.turn.pending_questions_json)[0];
  const oldPrompt = typeof oldPending.payload === "string" ? JSON.parse(oldPending.payload) : oldPending.payload;
  const oldDecision = { run_id: oldPrompt.run_id, candidate_revision: oldPrompt.revision, candidate_hash: oldPrompt.hash, decision: "approve", analyze_limit: 1, choices: oldPrompt.candidate_ids.map((id: string) => ({ candidate_id: id, selected: true, reason: "纳入" })) };
  await review.getByRole("button", { name: "全部排除", exact: true }).click();
  await expect(review.getByRole("button", { name: "确认纳入并继续", exact: true })).toBeDisabled();
  await review.getByRole("button", { name: "全部排除并重新检索", exact: true }).click();
  await expect(review.getByText(`确认候选论文 · v${oldPrompt.revision}`, { exact: true })).not.toBeVisible();
  await expect(review).toBeVisible();
  const stale = await request.post(`${sessionURL}/turns/${oldSnapshot.turn.id}/resume`, { data: { revision: oldSnapshot.turn.revision, idempotency_key: crypto.randomUUID(), answers: [{ interrupt_id: oldPending.id, research_candidates: oldDecision }] } });
  expect(stale.status()).toBe(409);
  await page.reload();
  await expect(review.getByRole("checkbox", { name: "纳入 Fixture Research" })).toBeChecked();
  await review.getByText("查看摘要", { exact: true }).click();
  await expect(review.getByText("A fixture paper for reproducible research workflows.", { exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("candidate-review.png"), fullPage: true });
  const fresh = await (await request.get(`${sessionURL}/snapshot`)).json();
  const pending = pendingJSON(fresh.turn.pending_questions_json)[0];
  const prompt = typeof pending.payload === "string" ? JSON.parse(pending.payload) : pending.payload;
  const data = { revision: fresh.turn.revision, idempotency_key: crypto.randomUUID(), answers: [{ interrupt_id: pending.id, research_candidates: { ...oldDecision, candidate_revision: prompt.revision, candidate_hash: prompt.hash } }] };
  expect((await request.post(`${sessionURL}/turns/${fresh.turn.id}/resume`, { data })).status()).toBe(202);
  expect((await request.post(`${sessionURL}/turns/${fresh.turn.id}/resume`, { data })).status()).toBe(202);
  await expect(page.locator('.research-task-status[data-state="completed"]')).toHaveText("已完成");
  const papers = await (await request.get(`/api/projects/${project.id}/papers`)).json();
  expect(papers.items).toHaveLength(1);
  const runs = await (await request.get(`/api/projects/${project.id}/research`)).json();
  expect(runs.items[0].candidate_round).toBe(2);
});

test("没有候选时仍提供重新检索和取消入口", async ({ page, request }) => {
  const project = await (await request.post("/api/projects", { data: { title: "无结果审核" } })).json();
  await page.goto(`/p/${project.id}/research`);
  await page.getByRole("textbox", { name: "调研问题", exact: true }).fill("无结果场景");
  await page.getByRole("button", { name: "开始调研", exact: true }).click();
  await page.getByRole("textbox", { name: "检索式", exact: true }).fill("no-matching-fixture");
  await page.getByRole("button", { name: "确认计划并继续", exact: true }).click();
  const review = page.getByRole("region", { name: "候选论文审核" });
  await expect(review).toBeVisible();
  await expect(review.getByRole("button", { name: "确认纳入并继续", exact: true })).toBeDisabled();
  await expect(review.getByRole("button", { name: "全部排除并重新检索", exact: true })).toBeEnabled();
  await review.getByRole("button", { name: "取消本次调研", exact: true }).click();
  await expect(page.locator('.research-task-status[data-state="completed"]')).toHaveText("已完成");
});

test("开始调研、修改计划、确认后刷新，执行身份与计划版本分别展示", async ({ page, request }, testInfo) => {
  await request.put("/api/runtime/model", { data: { mode: "fixture", endpoint: "", model: "", api_key: "" } });
  const project = await (await request.post("/api/projects", { data: { title: "研究计划验收" } })).json();
  await page.goto(`/p/${project.id}/research`);
  await page.getByRole("textbox", { name: "调研问题", exact: true }).fill("比较单卡条件下的时序预测基线");
  await page.getByRole("button", { name: "开始调研", exact: true }).click();
  const review = page.getByRole("region", { name: "研究计划审核" });
  await expect(review).toBeVisible();
  await page.reload();
  await expect(review).toBeVisible();
  await review.getByRole("textbox", { name: "检索式", exact: true }).fill("fixture");
  await review.getByRole("button", { name: "确认计划并继续", exact: true }).click();
  const candidates = page.getByRole("region", { name: "候选论文审核" });
  await expect(candidates).toBeVisible();
  await page.reload();
  await expect(candidates).toBeVisible();
  await candidates.getByRole("textbox", { name: "Fixture Research 的审核理由", exact: true }).fill("用户确认纳入：可复现基线");
  await candidates.getByRole("spinbutton", { name: "本轮分析上限" }).fill("1");
  const operationIDs: string[] = [];
  await page.route("**/turns/*/resume", async (route) => {
    const body = route.request().postDataJSON();
    if (body.answers?.[0]?.research_candidates?.decision === "approve") {
      operationIDs.push(body.idempotency_key);
      if (operationIDs.length === 1) { await route.abort("failed"); return; }
    }
    await route.continue();
  });
  await candidates.getByRole("button", { name: "确认纳入并继续", exact: true }).click();
  await expect(candidates.getByRole("alert")).toBeVisible();
  await expect(candidates.getByRole("button", { name: "确认纳入并继续", exact: true })).toBeEnabled();
  await candidates.getByRole("button", { name: "确认纳入并继续", exact: true }).click();
  await expect(page.locator('.research-task-status[data-state="completed"]')).toHaveText("已完成");
  expect(operationIDs).toHaveLength(2);
  expect(operationIDs[0]).toBe(operationIDs[1]);
  await page.goto(`/p/${project.id}/research`);
  await expect(page.getByText("报告已保存", { exact: true })).toBeVisible();
  await expect(page.getByText("计划 v2 · 已确认", { exact: true })).toBeVisible();
  await expect(page.getByText("fixture", { exact: true })).toBeVisible();
  const runs = await (await request.get(`/api/projects/${project.id}/research`)).json();
  expect(runs.items).toHaveLength(1);
  expect(runs.items[0].approved_plan_revision).toBe(2);
  expect(runs.items[0].candidate_review.choices[0].reason).toBe("用户确认纳入：可复现基线");
  expect(runs.items[0].candidate_review.analyze_limit).toBe(1);
  expect(runs.items[0].model_calls).toBeGreaterThanOrEqual(3);
  await page.screenshot({ path: testInfo.outputPath("research-plan-approved.png"), fullPage: true });
});

test("取消研究计划会恢复原调用并保留取消记录", async ({ page, request }) => {
  const project = await (await request.post("/api/projects", { data: { title: "取消调研" } })).json();
  await page.goto(`/p/${project.id}/research`);
  await page.getByRole("textbox", { name: "调研问题", exact: true }).fill("暂不开展的调研");
  await page.getByRole("button", { name: "开始调研", exact: true }).click();
  const review = page.getByRole("region", { name: "研究计划审核" });
  await expect(review).toBeVisible();
  await review.getByRole("button", { name: "取消本次调研", exact: true }).click();
  await expect(page.locator('.research-task-status[data-state="completed"]')).toHaveText("已完成");
  await page.goto(`/p/${project.id}/research`);
  await expect(page.getByText("已取消", { exact: true })).toBeVisible();
});

test("真实协议脚本模型纠正越序调用、通过审核后检索并累计 Usage", async ({ page, request }) => {
  const project = await (await request.post("/api/projects", { data: { title: "调研工具与完成检查" } })).json();
  let calls = 0;
  const observations: { blocked?: boolean; incomplete?: boolean; reviewed?: boolean; candidates?: boolean; excluded?: boolean; reportRequired?: boolean } = {};
  let candidateResult: { candidate_revision: number; candidates: { id: string }[] } | null = null;
  let selectedPaper = "";
  const model = createServer(async (incoming, response) => {
    let body = "";
    for await (const chunk of incoming) body += chunk;
    const input = JSON.parse(body);
    calls++;
    const last = input.messages.at(-1);
    const text = typeof last.content === "string" ? last.content : (last.content || []).map((item: { text?: string }) => item.text || "").join("");
    let call: { name: string; arguments: string } | null = null;
    let content = "";
    if (calls === 1) call = { name: "search_papers", arguments: '{"text":"forecasting","limit":1}' };
    if (calls === 2) { observations.blocked = text.includes("plan_not_approved"); content = "我已经完成了。"; }
    if (calls === 3) {
      observations.incomplete = text.includes("应用验收反馈");
      call = { name: "submit_research_result", arguments: '{"kind":"plan","expected_revision":0,"plan":{"summary":"比较预测基线","queries":["forecasting"]}}' };
    }
    if (calls === 4) call = { name: "ask_user", arguments: '{"kind":"research_plan","expected_revision":1,"question":"请审核检索计划"}' };
    if (calls === 5) {
      const result = JSON.parse(text);
      observations.reviewed = result.research_plan?.plan_revision === 2 && result.research_plan?.plan.queries[0] === "fixture";
      call = { name: "search_papers", arguments: '{"text":"fixture","limit":1}' };
    }
    if (calls === 6) { candidateResult = JSON.parse(text); content = "我已经完成调研了。"; }
    if (calls === 7) {
      call = { name: "ask_user", arguments: JSON.stringify({ kind: "research_candidates", question: "请审核检索候选", expected_revision: candidateResult!.candidate_revision, recommendations: candidateResult!.candidates.map((item) => ({ candidate_id: item.id, recommended: true, reason: "可复现基线" })) }) };
    }
    if (calls === 8) {
      const result = JSON.parse(text);
      selectedPaper = result.research_candidates?.choices[0].paper_id;
      observations.candidates = result.research_candidates?.analyze_limit === 1 && !!selectedPaper;
      call = { name: "read_paper", arguments: '{"paper_id":"excluded-paper","section":"abstract"}' };
    }
    if (calls === 9) {
      observations.excluded = text.includes("未被本任务纳入");
      call = { name: "read_paper", arguments: JSON.stringify({ paper_id: selectedPaper, section: "abstract" }) };
    }
    if (calls === 10) content = "我已完成研究。";
    if (calls === 11) {
      const runs = await (await request.get(`/api/projects/${project.id}/research`)).json();
      const work = runs.items[0].work_items[0];
      call = { name: "submit_research_result", arguments: JSON.stringify({ kind: "paper_analysis", expected_revision: work.revision,
        paper_analysis: { paper_id: selectedPaper, document_id: work.document_id, status: "completed", summary: "仅阅读摘要，实验细节未核实", fields: ["method", "datasets", "metrics", "code", "reproduction"].map((name) => ({ name, status: "unknown", text: "摘要未核实" })) } }) };
    }
    if (calls === 12) content = "已保存摘要级分析，实验细节未知。";
    if (calls === 13) {
      observations.reportRequired = text.includes("报告尚未保存");
      const run = (await (await request.get(`/api/projects/${project.id}/research`)).json()).items[0];
      const matrix = await (await request.get(`/api/projects/${project.id}/research/${run.id}/matrix`)).json();
      const work = run.work_items[0];
      call = { name: "submit_research_result", arguments: JSON.stringify({ kind: "report", expected_revision: 0, report: {
        title: "本地脚本报告", summary: "仅摘要级流程验证，实验细节未知。", matrix_hash: matrix.hash,
        limitations: ["本地协议脚本；仅阅读摘要，未运行实验。"],
        claims: ["comparison", "conflicts", "gaps", "next_steps"].map((section) => ({ section, status: "unknown", text: "原文与实验细节尚待核实。", analysis_refs: [{ work_item_id: work.id, analysis_revision: work.analysis_revision }] })),
      } }) };
    }
    if (calls === 14) content = "报告与矩阵已保存，实验细节未知。";
    const delta = call ? { role: "assistant", tool_calls: [{ index: 0, id: `research-${calls}`, type: "function", function: call }] } : { role: "assistant", content };
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\ndata: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: call ? "tool_calls" : "stop" }] })}\n\ndata: ${JSON.stringify({ choices: [], usage: { prompt_tokens: 12, completion_tokens: 3, total_tokens: 15 } })}\n\ndata: [DONE]\n\n`);
  });
  await new Promise<void>((resolve) => model.listen(0, "127.0.0.1", resolve));
  try {
    const address = model.address();
    if (!address || typeof address === "string") throw new Error("脚本模型未启动");
    await request.put("/api/runtime/model", { data: { mode: "remote", endpoint: `http://127.0.0.1:${address.port}/v1`, model: "research-test", api_key: "test-only" } });
    await page.goto(`/p/${project.id}/research`);
    await page.getByRole("textbox", { name: "调研问题", exact: true }).fill("研究时序预测基线");
    await page.getByRole("button", { name: "开始调研", exact: true }).click();
    const review = page.getByRole("region", { name: "研究计划审核" });
    await expect(review).toBeVisible();
    expect(calls).toBe(4);
    await review.getByRole("textbox", { name: "检索式", exact: true }).fill("fixture");
    await review.getByRole("button", { name: "确认计划并继续", exact: true }).click();
    const candidates = page.getByRole("region", { name: "候选论文审核" });
    await expect(candidates).toBeVisible();
    await candidates.getByRole("spinbutton", { name: "本轮分析上限" }).fill("1");
    await candidates.getByRole("button", { name: "确认纳入并继续", exact: true }).click();
    await expect(page.locator('.research-task-status[data-state="completed"]')).toHaveText("已完成");
    expect(observations).toEqual({ blocked: true, incomplete: true, reviewed: true, candidates: true, excluded: true, reportRequired: true });
    const runs = await (await request.get(`/api/projects/${project.id}/research`)).json();
    expect(runs.items[0].model_calls).toBe(14);
    expect(runs.items[0].input_tokens).toBe(168);
    expect(runs.items[0].output_tokens).toBe(42);
    expect(runs.items[0].unknown_usage).toBe(0);
    expect(runs.items[0].phase).toBe("reported");
  } finally {
    await request.put("/api/runtime/model", { data: { mode: "fixture", endpoint: "", model: "", api_key: "" } });
    model.closeAllConnections();
    await new Promise<void>((resolve) => model.close(() => resolve()));
  }
});
