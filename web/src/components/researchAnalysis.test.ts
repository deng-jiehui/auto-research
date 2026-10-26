import { describe, expect, it } from "vitest";
import { analysisProgress } from "./researchAnalysis";
import type { ResearchWorkItem } from "../types";

describe("逐篇进度", () => {
  it("失败单独计数，不能冒充已完成", () => {
    const items = [{ state: "completed" }, { state: "failed" }, { state: "reading", title: "第三篇" }, { state: "pending" }] as ResearchWorkItem[];
    expect(analysisProgress(items)).toEqual({ completed: 1, failed: 1, total: 4, current: "第三篇" });
  });
});
