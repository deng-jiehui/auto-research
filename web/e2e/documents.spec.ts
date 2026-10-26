import { test, expect } from "@playwright/test";
import { createServer } from "node:http";

test("获取全文、分块阅读、保存引用并在刷新后定位原文", async ({ page, request }, testInfo) => {
  const project = await (await request.post("/api/projects", { data: { title: "全文与证据验收" } })).json();
  const paper = await (await request.post(`/api/projects/${project.id}/papers`, { data: { title: "Fixture Research", arxiv_id: "2401.00001", abstract: "离线摘要" } })).json();
  await page.goto(`/p/${project.id}/papers?paper=${paper.id}`);
  await page.getByRole("button", { name: "获取全文", exact: true }).click();
  await expect(page.getByRole("region", { name: "论文原文阅读器" })).toBeVisible();
  await expect(page.getByRole("region", { name: "论文原文阅读器" })).toContainText("全文");
  await page.getByRole("button", { name: "Method · b4", exact: true }).click();
  await expect(page.locator(".document-block-text")).toContainText("linear baseline");
  await page.getByRole("button", { name: "引用本段", exact: true }).click();
  await page.getByRole("textbox", { name: "证据字段", exact: true }).fill("method");
  await page.getByRole("button", { name: "保存证据", exact: true }).click();
  await expect(page.getByRole("status").filter({ hasText: "证据已保存" })).toBeVisible();
  await expect(page.locator(".paper-evidence blockquote")).toContainText("linear baseline");
  await page.getByRole("link", { name: "查看原文", exact: true }).click();
  await expect(page.locator(".document-block-text mark")).toContainText("linear baseline");
  await page.reload();
  await expect(page.locator(".document-block-text mark")).toContainText("linear baseline");
  await page.screenshot({ path: testInfo.outputPath("document-citation.png"), fullPage: true });
  const docs = await (await request.get(`/api/projects/${project.id}/papers/${paper.id}/documents`)).json();
  expect(docs.items[0].text).toBe("");
  const evidence = await (await request.get(`/api/projects/${project.id}/papers/${paper.id}/evidence`)).json();
  expect(evidence.items).toHaveLength(1);
  expect(evidence.items[0].block_id).toBe("b4");
});

test("同一 Agent 按块阅读、检索、保存证据并在会话打开引用", async ({ page, request }) => {
  const project = await (await request.post("/api/projects", { data: { title: "证据工具验收" } })).json();
  const paper = await (await request.post(`/api/projects/${project.id}/papers`, { data: { title: "Fixture Research", arxiv_id: "2401.00001" } })).json();
  const session = await (await request.post(`/api/projects/${project.id}/sessions`, { data: { title: "阅读论文" } })).json();
  const calls: string[] = [];
  let documentID = "";
  let quote = "";
  let citation = "";
  const model = createServer(async (incoming, response) => {
    let body = "";
    for await (const chunk of incoming) body += chunk;
    const input = JSON.parse(body);
    const results = input.messages.filter((item: { role: string }) => item.role === "tool");
    const content = results.at(-1)?.content;
    const text = typeof content === "string" ? content : (content || []).map((item: { text?: string }) => item.text || "").join("");
    const result = text ? JSON.parse(text) : null;
    let call: { name: string; arguments: string } | null;
    if (results.length === 0) call = { name: "read_paper", arguments: JSON.stringify({ paper_id: paper.id, section: "fulltext" }) };
    else if (results.length === 1) {
      documentID = result.id;
      expect(result.text).toBe("");
      call = { name: "read_paper", arguments: JSON.stringify({ paper_id: paper.id, section: "block", document_id: documentID, block_id: "b4" }) };
    } else if (results.length === 2) {
      quote = result.text;
      call = { name: "search_project_evidence", arguments: '{"query":"linear baseline","limit":2}' };
    } else if (results.length === 3) {
      expect(result[0].document_id).toBe(documentID);
      call = { name: "save_evidence", arguments: JSON.stringify({ document_id: documentID, block_id: "b4", quote, field: "method", value: "同一训练划分比较线性与 Transformer" }) };
    } else {
      citation = result.citation_url;
      call = null;
    }
    if (call) calls.push(call.name);
    const delta = call ? { role: "assistant", tool_calls: [{ index: 0, id: `evidence-${results.length}`, type: "function", function: call }] }
      : { role: "assistant", content: `比较使用相同训练划分。[查看原文](${citation})` };
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\ndata: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: call ? "tool_calls" : "stop" }] })}\n\ndata: [DONE]\n\n`);
  });
  await new Promise<void>((resolve) => model.listen(0, "127.0.0.1", resolve));
  try {
    const address = model.address();
    if (!address || typeof address === "string") throw new Error("脚本模型未启动");
    expect((await request.put("/api/runtime/model", { data: { mode: "remote", endpoint: `http://127.0.0.1:${address.port}/v1`, model: "test-evidence", api_key: "test-only" } })).ok()).toBe(true);
    await page.goto(`/p/${project.id}/s/${session.id}`);
    await page.getByRole("textbox", { name: "消息", exact: true }).fill("阅读论文方法，搜索线性基线并保存原文证据，不保存项目笔记。");
    await page.getByRole("button", { name: "发送消息", exact: true }).click();
    await expect(page.getByText("completed", { exact: true })).toBeVisible();
    expect(calls).toEqual(["read_paper", "read_paper", "search_project_evidence", "save_evidence"]);
    await page.getByRole("link", { name: "查看原文", exact: true }).click();
    await expect(page.locator(".document-block-text mark")).toContainText("linear baseline");
    expect((await (await request.get(`/api/projects/${project.id}/notes`)).json()).items).toHaveLength(0);
  } finally {
    await request.put("/api/runtime/model", { data: { mode: "fixture", endpoint: "", model: "", api_key: "" } });
    model.closeAllConnections();
    await new Promise<void>((resolve) => model.close(() => resolve()));
  }
});
