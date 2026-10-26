import { describe, expect, it } from "vitest";
import { pendingInteraction } from "./interaction";
import type { SessionSnapshot } from "./types";

function snapshot(extra: Record<string, unknown> = {}): SessionSnapshot {
  return { turn: { id: "turn-1", session_id: "session-1", project_id: "project-1", input_text: "研究问题", state: "awaiting_input", revision: 4, pending_questions_json: JSON.stringify([{ id: "old", payload: { question: "旧内部数据" } }]) }, messages: [], events: [], notes: [], ...extra } as SessionSnapshot;
}

describe("共用 UI 交互协议", () => {
  it("结构化交互优先于原始检查点，保留编辑选项与自定义输入", () => {
    const result = pendingInteraction(snapshot({ ui: { version: "1", target: { kind: "research", id: "run-1" }, event_cursor: 9, available_actions: ["respond"], interaction: { id: "prompt-1", kind: "clarification", question: "选择计算资源", choices: ["单卡", "多卡"], allow_custom: true, placeholder: "也可自填" } } }));
    expect(result).toMatchObject({ id: "prompt-1", question: "选择计算资源", choices: ["单卡", "多卡"], allowCustom: true, placeholder: "也可自填" });
  });

  it("明确没有交互时不回退显示旧问题", () => {
    expect(pendingInteraction(snapshot({ ui: { version: "1", interaction: null } }))).toBeNull();
    expect(pendingInteraction(snapshot({ turn: { state: "completed" }, ui: { version: "1", interaction: { id: "stale", kind: "clarification", question: "旧问题" } } }))).toBeNull();
  });

  it("审核保持版本身份，未知类型和读取错误不可退化为普通回答", () => {
    const prompt = { id: "review-1", kind: "research_plan", question: "审核", run_id: "run-1", revision: 3, hash: "hash-3", plan: { summary: "基线", queries: ["forecast"] } };
    expect(pendingInteraction(snapshot({ ui: { version: "1", interaction: prompt } }))?.researchPlan).toMatchObject({ run_id: "run-1", revision: 3, hash: "hash-3" });
    expect(pendingInteraction(snapshot({ ui: { version: "1", interaction: { id: "future", kind: "new_approval", question: "未来交互" } } }))?.unsupported).toBe(true);
    expect(pendingInteraction(snapshot({ ui: { version: "1", interaction: null, interaction_error: "交互数据损坏" } }))?.unsupported).toBe(true);
    expect(pendingInteraction(snapshot({ ui: { version: "99", interaction: prompt } }))?.unsupported).toBe(true);
  });

  it("旧服务快照仍可读取持久中断", () => {
    expect(pendingInteraction(snapshot())).toMatchObject({ id: "old", question: "旧内部数据", allowCustom: true });
  });
});
