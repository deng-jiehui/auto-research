import type { ResearchWorkItem } from "../types";

export function analysisProgress(items: ResearchWorkItem[]) {
  return { completed: items.filter((item) => item.state === "completed").length,
    failed: items.filter((item) => item.state === "failed").length, total: items.length,
    current: items.find((item) => item.state === "reading")?.title || "" };
}
