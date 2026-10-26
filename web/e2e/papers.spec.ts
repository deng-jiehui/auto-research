import { test, expect } from "@playwright/test";
import { createServer } from "node:http";
import type { Server } from "node:http";

let model: Server;

// 使用本地脚本模型验收真实论文工具，不依赖离线演示模型的固定行为。
test.beforeEach(async ({ request }) => {
  model = createServer(async (incoming, response) => {
    let body = "";
    for await (const chunk of incoming) body += chunk;
    const input = JSON.parse(body);
    const results = input.messages.filter((item: { role: string }) => item.role === "tool");
    const content = results.at(-1)?.content;
    const text = typeof content === "string" ? content : (content || []).map((item: { text?: string }) => item.text || "").join("");
    const result = text ? JSON.parse(text) : null;
    const call = results.length === 0 ? { name: "search_papers", arguments: '{"text":"fixture","limit":1}' }
      : results.length === 1 ? { name: "save_paper", arguments: JSON.stringify({ reference: result[0].source_key }) }
      : { name: "read_paper", arguments: JSON.stringify({ paper_id: result.id }) };
    const complete = results.length >= 3;
    const delta = complete ? { role: "assistant", content: "已读取论文摘要并保存可定位来源。" }
      : { role: "assistant", tool_calls: [{ index: 0, id: `paper-${results.length}`, type: "function", function: call }] };
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`);
    response.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: complete ? "stop" : "tool_calls" }] })}\n\ndata: [DONE]\n\n`);
  });
  await new Promise<void>((resolve) => model.listen(0, "127.0.0.1", resolve));
  const address = model.address();
  if (!address || typeof address === "string") throw new Error("未获取测试模型端口");
  expect((await request.put("/api/runtime/model", { data: { mode: "remote", endpoint: `http://127.0.0.1:${address.port}/v1`, model: "test-papers", api_key: "test-only" } })).ok()).toBe(true);
});

test.afterEach(async ({ request }) => {
  await request.put("/api/runtime/model", { data: { mode: "fixture", endpoint: "", model: "", api_key: "" } });
  model.closeAllConnections();
  await new Promise<void>((resolve) => model.close(() => resolve()));
});

test("在会话中检索归档读取论文，刷新后可在论文库查看来源", async ({ page, request }) => {
  const project = await (await request.post("/api/projects", { data: { title: "论文会话验收" } })).json();
  const session = await (await request.post(`/api/projects/${project.id}/sessions`, { data: { title: "论文工具" } })).json();
  await page.goto(`/p/${project.id}/s/${session.id}`);
  await page.getByRole("textbox", { name: "消息", exact: true }).fill("搜索 fixture 论文，归档到项目并读取摘要");
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  await expect(page.getByText("completed", { exact: true })).toBeVisible();
  const tools = page.locator(".workspace-tools-list");
  await expect(page.getByRole("region", { name: "Agent 检索到的论文" })).toContainText("Fixture Research");
  await expect(tools).toContainText("save_paper");
  await expect(tools).toContainText("read_paper");
  await expect(page.locator(".conversation-message.assistant")).toContainText("已读取论文摘要");
  const papers = await (await request.get(`/api/projects/${project.id}/papers`)).json();
  expect(papers.items).toHaveLength(1);
  const docs = await (await request.get(`/api/projects/${project.id}/papers/${papers.items[0].id}/documents`)).json();
  expect(docs.items).toHaveLength(1);
  expect(docs.items[0].sha256).toHaveLength(64);
  await page.reload();
  await expect(page.locator(".workspace-tools-list")).toContainText("read_paper");
  await page.goto(`/p/${project.id}/papers`);
  await expect(page.getByText("来源：离线演示数据（fixture），只验证流程，不代表真实检索。")).toBeVisible();
  await page.getByRole("button", { name: "Fixture Research", exact: false }).click();
  await page.getByText("abstract", { exact: true }).click();
  await expect(page.locator(".paper-evidence")).toContainText("ten samples");
});

test("论文库搜索、显式归档和空结果", async ({ page, request }) => {
  const project = await (await request.post("/api/projects", { data: { title: "论文搜索验收" } })).json();
  await page.goto(`/p/${project.id}/papers`);
  await page.getByRole("textbox", { name: "搜索论文", exact: true }).fill("fixture");
  await page.getByRole("button", { name: "搜索", exact: true }).click();
  await expect(page.locator(".candidate-list")).toContainText("Fixture Research");
  expect((await (await request.get(`/api/projects/${project.id}/papers`)).json()).items).toHaveLength(0);
  await page.getByRole("button", { name: "归档到项目", exact: true }).click();
  await expect(page.locator(".paper-list")).toContainText("Fixture Research");
  await expect(page.getByRole("button", { name: "已加入论文库", exact: true })).toBeDisabled();
  await page.getByRole("textbox", { name: "搜索论文", exact: true }).fill("no-matching-paper");
  await page.getByRole("button", { name: "搜索", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("没有找到匹配论文");
  await expect(page.locator(".candidate-list")).toHaveCount(0);
});
