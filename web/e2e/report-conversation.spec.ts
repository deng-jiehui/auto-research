import { test, expect, type APIRequestContext, type Page } from "@playwright/test";

async function createReport(page: Page, request: APIRequestContext, withOrigin: boolean) {
  await request.put("/api/runtime/model", { data: { mode: "fixture", endpoint: "", model: "", api_key: "" } });
  const project = await (await request.post("/api/projects", { data: { title: "报告对话验收" } })).json();
  const session = withOrigin ? await (await request.post(`/api/projects/${project.id}/sessions`, { data: { title: "来源讨论" } })).json() : null;
  const task = await (await request.post(`/api/projects/${project.id}/research`, { data: { question: "比较可复现的预测基线", origin_session_id: session?.id, idempotency_key: "start" } })).json();
  await page.goto(`/p/${project.id}/research/${task.run.id}`);
  await page.getByRole("button", { name: "确认计划并继续", exact: true }).click();
  await page.getByRole("button", { name: "确认纳入并继续", exact: true }).click();
  await expect(page.getByRole("button", { name: "导出 Markdown", exact: true })).toBeVisible();
  const run = await (await request.get(`/api/projects/${project.id}/research/${task.run.id}`)).json();
  const report = await (await request.get(`/api/projects/${project.id}/reports/${run.latest_report.id}`)).json();
  return { project, session, run, report };
}

test("报告进入来源会话，多轮追问和刷新保持引用，移除后停止继承", async ({ page, request }, testInfo) => {
  const { project, session, run, report } = await createReport(page, request, true);
  await page.getByRole("button", { name: "基于此报告提问", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/s/${session.id}\\?report=${report.id}$`));
  const card = page.getByRole("region", { name: "当前引用的研究报告" });
  await expect(card).toContainText(report.title);
  await expect(card).toContainText("v1");
  await page.getByRole("textbox", { name: "消息", exact: true }).fill("这份报告的依据是什么？");
  const sent = page.waitForResponse((res) => res.request().method() === "POST" && res.url().endsWith(`/sessions/${session.id}/messages`));
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  const first = await (await sent).json();
  expect(first.turn.report_context).toMatchObject({ report_id: report.id, run_id: run.id, revision: 1 });
  await expect(page.getByRole("textbox", { name: "消息", exact: true })).toBeEnabled();
  await expect(page.getByRole("link", { name: "查看原文", exact: true }).first()).toBeVisible();
  await page.reload();
  await expect(card).toContainText(report.title);
  await page.getByRole("textbox", { name: "消息", exact: true }).fill("进一步解释这个限制");
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  await expect(page.getByRole("textbox", { name: "消息", exact: true })).toBeEnabled();
  const snapshot = await (await request.get(`/api/projects/${project.id}/sessions/${session.id}/snapshot`)).json();
  expect(snapshot.turn.report_context.report_id).toBe(report.id);
  expect((await (await request.get(`/api/projects/${project.id}/research`)).json()).items).toHaveLength(1);
  await page.screenshot({ path: testInfo.outputPath("report-discussion.png"), fullPage: true, animations: "disabled" });
  await card.getByRole("button", { name: "移除报告引用", exact: true }).click();
  await expect(card).toHaveCount(0);
  await page.getByRole("textbox", { name: "消息", exact: true }).fill("我们换个话题，介绍一下实验记录");
  const cleared = page.waitForResponse((res) => res.request().method() === "POST" && res.url().endsWith("/messages"));
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  expect((await (await cleared).json()).turn.report_context).toBeUndefined();
  await page.reload();
  await expect(page.getByRole("textbox", { name: "消息", exact: true })).toBeVisible();
  await expect(card).toHaveCount(0);
});

test("来源会话缺失时新建讨论，报告深链可打开，跨项目引用被拒绝", async ({ page, request }) => {
  const { project, run, report } = await createReport(page, request, false);
  await page.getByRole("button", { name: "基于此报告提问", exact: true }).click();
  await expect(page).toHaveURL(/\/s\/[^/]+\?report=/);
  const card = page.getByRole("region", { name: "当前引用的研究报告" });
  await expect(card).toContainText(report.title);
  await card.getByRole("link", { name: "打开引用报告", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/research/${run.id}\\?report=${report.id}$`));
  await expect(page.getByRole("combobox", { name: "报告版本", exact: true })).toHaveValue(report.id);
  const foreign = await (await request.post("/api/projects", { data: { title: "其他项目" } })).json();
  const session = await (await request.post(`/api/projects/${foreign.id}/sessions`, { data: { title: "讨论" } })).json();
  const bad = await request.post(`/api/projects/${foreign.id}/sessions/${session.id}/messages`, { data: { text: "分析此报告", idempotency_key: "foreign", report_context: { run_id: run.id, report_id: report.id } } });
  expect(bad.status()).toBe(404);
  await page.goto(`/p/${foreign.id}/s/${session.id}?report=${report.id}`);
  await expect(page.getByRole("alert").filter({ hasText: "报告" })).toBeVisible();
  await page.getByRole("textbox", { name: "消息", exact: true }).fill("分析此报告");
  await expect(page.getByRole("button", { name: "发送消息", exact: true })).toBeDisabled();
});

test("来源会话删除后仍能基于报告新建讨论，手机布局可用", async ({ page, request }, testInfo) => {
  const { project, session, report } = await createReport(page, request, true);
  expect((await request.delete(`/api/projects/${project.id}/sessions/${session.id}`)).status()).toBe(204);
  await page.getByRole("button", { name: "基于此报告提问", exact: true }).click();
  await expect(page).toHaveURL(/\/s\/[^/]+\?report=/);
  expect(new URL(page.url()).pathname).not.toContain(session.id);
  const card = page.getByRole("region", { name: "当前引用的研究报告" });
  await expect(card).toContainText(report.title);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole("textbox", { name: "消息", exact: true })).toBeVisible();
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(2);
  await page.screenshot({ path: testInfo.outputPath("report-discussion-mobile.png"), fullPage: true, animations: "disabled" });
});

test("新版目录提示不会自动改变讨论版本，切换后发送相应身份，网络失败保留草稿", async ({ page, request }) => {
  const { project, session, run, report } = await createReport(page, request, true);
  // 仅模拟新版资料目录；真实报告持久化与版本切换由 SQLite 集成测试覆盖。
  const next = { ...report, id: "report-version-2", revision: 2, title: "新版实验建议", resource_path: "research://reports/report-version-2/report.md" };
  await page.route(`**/api/projects/${project.id}/research/${run.id}/reports`, (route) => route.fulfill({ json: { items: [next, report] } }));
  await page.route(`**/api/projects/${project.id}/reports/${next.id}`, (route) => route.fulfill({ json: next }));
  await page.getByRole("button", { name: "基于此报告提问", exact: true }).click();
  const card = page.getByRole("region", { name: "当前引用的研究报告" });
  await expect(card.getByRole("status")).toContainText("已有 v2，当前仍讨论 v1");
  await expect(card.getByRole("combobox", { name: "讨论报告版本", exact: true })).toHaveValue(report.id);
  await card.getByRole("button", { name: "切换到新版", exact: true }).click();
  await expect(card).toContainText(next.title);
  await expect(card.getByRole("combobox", { name: "讨论报告版本", exact: true })).toHaveValue(next.id);
  await page.route(`**/api/projects/${project.id}/sessions/${session.id}/messages`, (route) => route.abort("internetdisconnected"));
  await page.getByRole("textbox", { name: "消息", exact: true }).fill("解释新版实验建议");
  const sent = page.waitForRequest((req) => req.method() === "POST" && req.url().endsWith(`/sessions/${session.id}/messages`));
  await page.getByRole("button", { name: "发送消息", exact: true }).click();
  expect((await sent).postDataJSON().report_context).toEqual({ run_id: run.id, report_id: next.id });
  await expect(page.getByRole("alert")).toContainText("无法连接");
  await expect(card).toContainText(next.title);
  await expect(page.getByRole("textbox", { name: "消息", exact: true })).toHaveValue("解释新版实验建议");
});
