import { test, expect } from "@playwright/test";

test("研究页面仅靠共用 UI 协议完成审核，不读取原始检查点", async ({ page, request }, testInfo) => {
  await request.put("/api/runtime/model", { data: { mode: "fixture", endpoint: "", model: "", api_key: "" } });
  const project = await (await request.post("/api/projects", { data: { title: "共用交互层验收" } })).json();
  const created = await (await request.post(`/api/projects/${project.id}/research`, { data: { question: "比较可重复的预测基线", idempotency_key: "ui-start" } })).json();
  const answers: string[] = [];
  const streams: string[] = [];
  page.on("request", (req) => {
    if (req.method() === "POST" && req.url().endsWith("/resume")) answers.push(req.url());
    if (req.url().includes(`/research/${created.run.id}/events`) && req.headers().accept === "text/event-stream") streams.push(req.url());
  });
  await page.route(`**/research/${created.run.id}/snapshot`, async (route) => {
    const response = await route.fetch();
    const data = await response.json();
    expect(data.ui.version).toBe("1");
    expect(data.ui.target).toEqual({ kind: "research", id: created.run.id });
    // 明确移除旧格式，防止前端仍偷偷依赖图内部中断。
    delete data.turn.pending_questions_json;
    await route.fulfill({ response, json: data });
  });
  await page.goto(`/p/${project.id}/research/${created.run.id}`);
  await expect(page.getByRole("button", { name: "确认计划并继续", exact: true })).toBeVisible();
  await page.reload();
  await page.getByRole("textbox", { name: "计划说明", exact: true }).fill("核对可重复的预测基线和单卡条件");
  await page.getByRole("button", { name: "确认计划并继续", exact: true }).click();
  await page.getByRole("button", { name: "确认纳入并继续", exact: true }).click();
  await expect(page.getByRole("heading", { name: "方法比较", exact: true })).toBeVisible();
  expect(answers).toHaveLength(2);
  expect(answers.every((url) => url.includes(`/research/${created.run.id}/turns/`))).toBe(true);
  expect(streams.length).toBeGreaterThan(0);
  await page.screenshot({ path: testInfo.outputPath("shared-ui-research.png"), fullPage: true, animations: "disabled" });
});

test("会话共用交互支持编辑选项、自定义回答，未知交互只展示提示", async ({ page, request }, testInfo) => {
  await request.put("/api/runtime/model", { data: { mode: "fixture", endpoint: "", model: "", api_key: "" } });
  const project = await (await request.post("/api/projects", { data: { title: "共用会话交互" } })).json();
  const session = await (await request.post(`/api/projects/${project.id}/sessions`, { data: { title: "交互验收" } })).json();
  let unsupported = false;
  await page.route(`**/sessions/${session.id}/snapshot*`, async (route) => {
    const response = await route.fetch();
    if (response.status() !== 200) { await route.fulfill({ response }); return; }
    const data = await response.json();
    delete data.turn.pending_questions_json;
    if (unsupported && data.ui?.interaction) data.ui.interaction.kind = "future_approval";
    await route.fulfill({ response, json: data });
  });
  await page.goto(`/p/${project.id}/s/${session.id}`);
  await page.getByRole("textbox", { name: "消息", exact: true }).fill("请帮我整理研究方向并保存为项目笔记");
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  const choice = page.getByRole("textbox", { name: "编辑选项 1", exact: true });
  await expect(choice).toBeVisible();
  await page.getByRole("radio", { name: "选择选项 1", exact: true }).check();
  await choice.fill("研究单卡可复现的时序预测");
  await expect(page.getByRole("textbox", { name: "补充回答", exact: true })).toHaveValue("研究单卡可复现的时序预测");
  unsupported = true;
  await page.reload();
  await expect(page.getByText("暂时无法处理此交互", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "提交回答", exact: true })).toHaveCount(0);
  unsupported = false;
  await page.reload();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole("textbox", { name: "补充回答", exact: true })).toBeVisible();
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(2);
  await page.screenshot({ path: testInfo.outputPath("shared-ui-clarification-mobile.png"), fullPage: true, animations: "disabled" });
  await page.getByRole("textbox", { name: "补充回答", exact: true }).fill("研究单卡可复现的时序预测，先比较基线");
  await page.getByRole("button", { name: "提交回答", exact: true }).click();
  await expect(page.getByText("completed", { exact: true })).toBeVisible();
});
