import { createServer } from "node:http";
import { test, expect } from "@playwright/test";

test("会话模式按钮与检索结果快捷入库形成闭环", async ({ page, request }) => {
  const modelCalls: boolean[] = [];
  // 本地模型只负责发出工具调用，论文仍由真实应用的 fixture 来源提供。
  const model = createServer(async (incoming, response) => {
    let body = "";
    for await (const chunk of incoming) body += chunk;
    const input = JSON.parse(body);
    const delegates = (input.tools || []).some((item: { function: { name: string } }) => item.function.name === "run_subagent");
    modelCalls.push(delegates);
    const last = input.messages.at(-1);
    const lastText = typeof last.content === "string" ? last.content : (last.content || []).map((item: { text?: string }) => item.text || "").join("");
    const delegation = delegates && last.role !== "tool" && lastText.includes("委派");
    const delta = last.role === "tool"
      ? { role: "assistant", content: "检索到 Fixture Research，仅阅读摘要，尚未加入论文库。" }
      : { role: "assistant", tool_calls: [{ index: 0, id: "search-browser", type: "function", function: delegation ? { name: "run_subagent", arguments: '{"task":"检索 fixture 论文，不归档"}' } : { name: "search_papers", arguments: '{"text":"fixture","limit":2}' } }] };
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.write(`data: ${JSON.stringify({ id: "local-test", choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`);
    response.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: last.role === "tool" ? "stop" : "tool_calls" }] })}\n\ndata: [DONE]\n\n`);
  });
  await new Promise<void>((resolve) => model.listen(0, "127.0.0.1", resolve));
  const address = model.address();
  if (!address || typeof address === "string") throw new Error("未获取模型测试端口");
  try {
    expect((await request.put("/api/runtime/model", { data: { mode: "remote", endpoint: `http://127.0.0.1:${address.port}/v1`, model: "local-test", api_key: "test-only" } })).ok()).toBe(true);
    const project = await (await request.post("/api/projects", { data: { title: "会话入库验收" } })).json();
    const session = await (await request.post(`/api/projects/${project.id}/sessions`, { data: { title: "搜索论文" } })).json();
    await page.goto(`/p/${project.id}/s/${session.id}`);
    const multi = page.getByRole("button", { name: "多 Agent", exact: true });
    await expect(multi).toBeEnabled({ timeout: 2500 });
    await multi.click();
    await expect(multi).toHaveAttribute("aria-pressed", "true");
    await page.reload();
    await expect(page.getByRole("button", { name: "多 Agent", exact: true })).toHaveAttribute("aria-pressed", "true");
    await page.getByRole("textbox", { name: "消息", exact: true }).fill("检索 fixture 论文，先不要归档");
    await page.getByRole("button", { name: "发送消息", exact: true }).click();
    await expect(page.locator(".paper-search-results")).toContainText("Fixture Research");
    await expect(page.getByText("completed", { exact: true })).toBeVisible();
    expect(modelCalls.every(Boolean)).toBe(true);
    expect((await (await request.get(`/api/projects/${project.id}/papers`)).json()).items).toHaveLength(0);
    await page.screenshot({ path: test.info().outputPath("agent-controls.png"), fullPage: true });
    await page.route(`**/api/projects/${project.id}/papers`, async (route) => {
      if (route.request().method() === "POST") {
        await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ code: "unavailable", message: "测试归档暂时不可用" }) });
      } else await route.continue();
    }, { times: 1 });
    await page.getByRole("button", { name: "加入论文库", exact: true }).click();
    await expect(page.locator(".paper-search-results").getByRole("alert")).toContainText("暂时不可用");
    await page.getByRole("button", { name: "加入论文库", exact: true }).click();
    await expect(page.getByRole("button", { name: "已加入论文库", exact: true })).toBeDisabled();
    const references = page.getByRole("complementary", { name: "研究上下文" });
    await expect(references).toContainText("Fixture Research");
    const saved = (await (await request.get(`/api/projects/${project.id}/papers`)).json()).items;
    expect(saved).toHaveLength(1);
    expect(saved[0].abstract).toContain("reproducible research");
    await page.reload();
    await expect(page.getByRole("button", { name: "已加入论文库", exact: true })).toBeDisabled();
    await page.getByRole("textbox", { name: "消息", exact: true }).fill("请委派子助手检索 fixture 论文");
    await page.getByRole("button", { name: "发送消息", exact: true }).click();
    await expect(page.locator(".workspace-tools-list")).toContainText("run_subagent");
    await expect(page.getByText("completed", { exact: true })).toBeVisible();
    await expect(page.locator(".paper-search-results")).toContainText("Fixture Research");
    await page.getByRole("complementary", { name: "研究上下文" }).getByRole("link", { name: "打开论文：Fixture Research" }).click();
    await expect(page.locator(".paper-detail h2")).toHaveText("Fixture Research");
    expect((await (await request.get(`/api/projects/${project.id}/papers`)).json()).items).toHaveLength(1);
    await page.goto(`/p/${project.id}/s/${session.id}`);
    await page.getByRole("button", { name: "单 Agent", exact: true }).click();
    const responsePromise = page.waitForResponse((response) => response.url().endsWith("/messages") && response.request().method() === "POST");
    await page.getByRole("textbox", { name: "消息", exact: true }).fill("再检索一次 fixture");
    await page.getByRole("button", { name: "发送消息", exact: true }).click();
    const sent = await (await responsePromise).json();
    const context = JSON.parse(Buffer.from(sent.turn.context_json, "base64").toString("utf8"));
    expect(context.agent_mode).toBe("single");
    await expect.poll(() => modelCalls.at(-1)).toBe(false);
    await expect(page.getByText("completed", { exact: true })).toBeVisible();
    await page.getByRole("link", { name: "论文库", exact: true }).first().click();
    await expect(page.getByRole("heading", { name: /论文库/ })).toBeVisible();
  } finally {
    await request.put("/api/runtime/model", { data: { mode: "fixture", endpoint: "", model: "", api_key: "" } });
    model.closeAllConnections();
    await new Promise<void>((resolve) => model.close(() => resolve()));
  }
});
