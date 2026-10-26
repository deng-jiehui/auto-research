import { createServer } from "node:http";
import { test, expect } from "@playwright/test";

test("编辑原消息重新调用 Agent 并替换后续对话", async ({ page, request }) => {
  let calls = 0;
  const prompts: string[] = [];
  let release: (() => void) | undefined;
  const model = createServer(async (incoming, response) => {
    let body = "";
    for await (const chunk of incoming) body += chunk;
    const input = JSON.parse(body);
    prompts.push(JSON.stringify(input.messages));
    const number = ++calls;
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.flushHeaders();
    // 暂停重跑响应，以便检查旧内容在新回答生成前就已移除。
    if (number === 4) await new Promise<void>((resolve) => { release = resolve; });
    response.write(`data: ${JSON.stringify({ choices: [{ index: 0, delta: { role: "assistant", content: `本地测试回答-${number}` }, finish_reason: null }] })}\n\n`);
    response.end(`data: ${JSON.stringify({ choices: [{ index: 0, delta: {}, finish_reason: "stop" }] })}\n\ndata: [DONE]\n\n`);
  });
  await new Promise<void>((resolve) => model.listen(0, "127.0.0.1", resolve));
  const address = model.address();
  if (!address || typeof address === "string") throw new Error("未获取本地模型端口");
  try {
    expect((await request.put("/api/runtime/model", { data: { mode: "remote", endpoint: `http://127.0.0.1:${address.port}/v1`, model: "message-edit", api_key: "test-only" } })).ok()).toBe(true);
    const project = await (await request.post("/api/projects", { data: { title: "消息编辑验收" } })).json();
    const session = await (await request.post(`/api/projects/${project.id}/sessions`, { data: { title: "连续研究" } })).json();
    await page.goto(`/p/${project.id}/s/${session.id}`);
    for (const text of ["保留前文", "原始研究问题", "后续旧问题"]) {
      await page.getByRole("textbox", { name: "消息", exact: true }).fill(text);
      await page.getByRole("button", { name: "发送消息", exact: true }).click();
      await expect(page.getByText("completed", { exact: true })).toBeVisible();
      await expect(page.locator(".conversation-message.assistant").last()).toHaveText(new RegExp(`本地测试回答-${calls}`));
    }
    const source = page.locator(".conversation-message.user").filter({ hasText: "原始研究问题" });
    await source.getByRole("button", { name: "编辑消息", exact: true }).click();
    await expect(page.getByRole("textbox", { name: "修改消息", exact: true })).toHaveValue("原始研究问题");
    await page.getByRole("textbox", { name: "修改消息", exact: true }).fill("取消的修改");
    await page.getByRole("button", { name: "取消编辑", exact: true }).click();
    await expect(source).toContainText("原始研究问题");
    expect(calls).toBe(3);
    await source.getByRole("button", { name: "编辑消息", exact: true }).click();
    const editor = page.getByRole("textbox", { name: "修改消息", exact: true });
    const save = page.getByRole("button", { name: "保存并重新发送", exact: true });
    await editor.fill(" ");
    await expect(save).toBeDisabled();
    await editor.fill("修改后的研究问题");
    await page.screenshot({ path: test.info().outputPath("message-editor.png"), fullPage: true });
    await page.route("**/turns/*/edit", (route) => route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ code: "unavailable", message: "测试提交失败" }) }), { times: 1 });
    await save.click();
    await expect(page.getByRole("alert")).toContainText("测试提交失败");
    await expect(editor).toHaveValue("修改后的研究问题");
    await expect(page.getByText("本地测试回答-2", { exact: true })).toBeVisible();
    await save.click();
    await expect.poll(() => calls).toBe(4);
    expect(prompts[3]).toContain("修改后的研究问题");
    expect(prompts[3]).not.toContain("后续旧问题");
    expect(prompts[3]).not.toContain("本地测试回答-2");
    await expect(page.getByText("修改后的研究问题", { exact: true })).toHaveCount(1);
    for (const removed of ["原始研究问题", "后续旧问题", "本地测试回答-2", "本地测试回答-3"]) {
      await expect(page.getByText(removed, { exact: true })).toHaveCount(0);
    }
    await expect(page.getByText("本地测试回答-1", { exact: true })).toBeVisible();
    await page.reload();
    await expect(page.getByText("修改后的研究问题", { exact: true })).toBeVisible();
    await expect(page.getByText("本地测试回答-2", { exact: true })).toHaveCount(0);
    release!();
    await expect(page.getByText("completed", { exact: true })).toBeVisible();
    await expect(page.getByText("本地测试回答-4", { exact: true })).toBeVisible();
    await page.reload();
    await expect(page.locator(".conversation-message.assistant")).toHaveCount(2);
    await expect(page.getByText("修改后的研究问题", { exact: true })).toHaveCount(1);
  } finally {
    release?.();
    try {
      await request.put("/api/runtime/model", { data: { mode: "fixture", endpoint: "", model: "", api_key: "" } });
    } finally {
      model.closeAllConnections();
      await new Promise<void>((resolve) => model.close(() => resolve()));
    }
  }
});
