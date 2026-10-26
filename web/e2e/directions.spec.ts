import { test, expect } from "@playwright/test";
import { createServer } from "node:http";

test("方向提案可编辑、确认、刷新并带版本继续会话", async ({ page, request }, testInfo) => {
  const project = await (await request.post("/api/projects", { data: { title: "研究方向验收" } })).json();
  const response = await request.post(`/api/projects/${project.id}/directions`, { data: { name: "少样本预测基线", description: "统一划分比较基线", questions: ["线性模型是否足够？"], idempotency_key: "direction-e2e" } });
  expect(response.status()).toBe(201);
  const proposal = await response.json();
  await page.goto(`/p/${project.id}/directions?direction=${proposal.id}`);
  await expect(page.getByText("依据不足：尚无代表论文", { exact: true })).toBeVisible();
  await page.getByRole("textbox", { name: "方向名称", exact: true }).fill("可重复的时序预测评测");
  await page.getByRole("button", { name: "保存修改", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("修改已保存");
  const stale = await request.post(`/api/projects/${project.id}/directions/${proposal.id}/accept`, { data: { revision: proposal.revision } });
  expect(stale.status()).toBe(409);
  await page.getByRole("button", { name: "确认方向", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("方向已确认");
  await page.reload();
  await expect(page.getByRole("textbox", { name: "方向名称", exact: true })).toHaveValue("可重复的时序预测评测");
  await expect(page.getByRole("button", { name: "继续研究", exact: true })).toBeEnabled();
  await page.getByText("历史版本", { exact: true }).click();
  await expect(page.locator(".direction-history")).toContainText("少样本预测基线");
  await page.screenshot({ path: testInfo.outputPath("direction-accepted.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.getByRole("button", { name: "继续研究", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/s/[^?]+\\?direction=${proposal.id}`));
  await expect(page.getByRole("textbox", { name: "消息", exact: true })).toContainText("可重复的时序预测评测");
});

test("Agent 使用现有读取工具保存方向提案，前端卡片进入用户确认", async ({ page, request }) => {
  const project = await (await request.post("/api/projects", { data: { title: "Agent 方向提案" } })).json();
  const session = await (await request.post(`/api/projects/${project.id}/sessions`, { data: { title: "整理方向" } })).json();
  const called: string[] = [];
  const model = createServer(async (incoming, response) => {
    let body = "";
    for await (const chunk of incoming) body += chunk;
    const input = JSON.parse(body);
    const results = input.messages.filter((item: { role: string }) => item.role === "tool");
    const content = results.at(-1)?.content;
    const text = typeof content === "string" ? content : (content || []).map((item: { text?: string }) => item.text || "").join("");
    const result = text ? JSON.parse(text) : null;
    const call = results.length === 0 ? { name: "read_file", arguments: '{"path":"research://index.json"}' }
      : results.length === 1 ? { name: "submit_research_result", arguments: '{"kind":"direction","direction":{"name":"多变量预测的线性基线","description":"比较统一数据划分与复杂模型","gap":"待验证小样本下的差异","feasibility":"单卡实验"}}' }
      : results.length === 2 ? { name: "read_file", arguments: JSON.stringify({ path: result.resource_path }) } : null;
    if (call) called.push(call.name);
    const delta = call ? { role: "assistant", tool_calls: [{ index: 0, id: `direction-${results.length}`, type: "function", function: call }] }
      : { role: "assistant", content: "已保存方向提案，请在卡片中检查并确认；目前缺少代表论文，研究空白仍待验证。" };
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta, finish_reason: null }] })}\n\ndata: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: call ? "tool_calls" : "stop" }] })}\n\ndata: [DONE]\n\n`);
  });
  await new Promise<void>((resolve) => model.listen(0, "127.0.0.1", resolve));
  try {
    const address = model.address();
    if (!address || typeof address === "string") throw new Error("脚本模型未启动");
    await request.put("/api/runtime/model", { data: { mode: "remote", endpoint: `http://127.0.0.1:${address.port}/v1`, model: "test-directions", api_key: "test-only" } });
    await page.goto(`/p/${project.id}/s/${session.id}`);
    await page.getByRole("textbox", { name: "消息", exact: true }).fill("整理并保存一个时序预测研究方向提案，不保存项目笔记。");
    await page.getByRole("button", { name: "发送消息", exact: true }).click();
    await expect(page.getByText("completed", { exact: true })).toBeVisible();
    expect(called).toEqual(["read_file", "submit_research_result", "read_file"]);
    await expect(page.getByRole("region", { name: "研究方向提案" })).toContainText("多变量预测的线性基线");
    await page.getByRole("link", { name: "查看并确认方向", exact: true }).click();
    await page.getByRole("button", { name: "确认方向", exact: true }).click();
    await expect(page.getByRole("status")).toContainText("方向已确认");
    const directions = await (await request.get(`/api/projects/${project.id}/directions`)).json();
    expect(directions.items).toHaveLength(1);
    expect(directions.items[0].state).toBe("accepted");
    expect(directions.items[0].source_session_id).toBe(session.id);
  } finally {
    await request.put("/api/runtime/model", { data: { mode: "fixture", endpoint: "", model: "", api_key: "" } });
    model.closeAllConnections();
    await new Promise<void>((resolve) => model.close(() => resolve()));
  }
});
