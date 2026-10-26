import type { CandidateReviewPrompt, ResearchPlanPrompt, SessionSnapshot, UIInteraction } from "./types";

export interface PendingQuestion {
  id: string;
  question: string;
  choices: string[];
  allowCustom: boolean;
  placeholder: string;
  researchPause?: string;
  researchPlan?: ResearchPlanPrompt;
  researchCandidates?: CandidateReviewPrompt;
  unsupported?: boolean;
}

function fromInteraction(value: UIInteraction): PendingQuestion {
  const result: PendingQuestion = { id: value.id, question: value.question, choices: value.choices || [], allowCustom: value.allow_custom !== false, placeholder: value.placeholder || "可以直接输入，或编辑/选择上方选项" };
  switch (value.kind) {
    case "clarification": break;
    case "research_plan":
      if (value.run_id && value.revision && value.hash && value.plan) result.researchPlan = value as ResearchPlanPrompt;
      else result.unsupported = true;
      break;
    case "research_candidates":
      if (value.run_id && value.revision && value.hash && value.analyze_limit) result.researchCandidates = value as CandidateReviewPrompt;
      else result.unsupported = true;
      break;
    case "research_pause": result.researchPause = value.run_id; if (!value.run_id) result.unsupported = true; break;
    default: result.unsupported = true;
  }
  return result;
}

export function pendingInteraction(snapshot?: SessionSnapshot | null): PendingQuestion | null {
  if (!snapshot || snapshot.turn.state !== "awaiting_input") return null;
  if (snapshot.ui) {
    if (snapshot.ui.version !== "1" || snapshot.ui.interaction_error) return { id: "", question: snapshot.ui.interaction_error || "当前客户端暂不支持此交互协议，请更新客户端。", choices: [], allowCustom: false, placeholder: "", unsupported: true };
    return snapshot.ui.interaction ? fromInteraction(snapshot.ui.interaction) : null;
  }
  return parsePendingQuestion(snapshot.turn.pending_questions_json);
}

function parseJSON(raw: string): unknown {
  try { return JSON.parse(raw); }
  catch {
    try { return JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(raw), (char) => char.charCodeAt(0)))); }
    catch { return null; }
  }
}

// 只用于兼容尚未提供 UI 协议的旧服务，新快照不会回退读取内部中断。
export function parsePendingQuestion(raw?: string): PendingQuestion | null {
  if (!raw) return null;
  const parsed = parseJSON(raw);
  if (!Array.isArray(parsed)) return null;
  const first = parsed[0] as { id?: unknown; payload?: unknown } | undefined;
  if (!first || typeof first.id !== "string" || !first.id.trim()) return null;
  const value = typeof first.payload === "string" ? parseJSON(first.payload) : first.payload;
  if (!value || typeof value !== "object") return null;
  const payload = value as UIInteraction;
  const choices = Array.isArray(payload.choices) ? payload.choices.map((item: unknown) => {
    if (typeof item === "string") return item.trim();
    if (!item || typeof item !== "object") return "";
    const record = item as Record<string, unknown>;
    return [record.label, record.title, record.text, record.value].find((candidate) => typeof candidate === "string" && candidate.trim()) || "";
  }).filter((item): item is string => typeof item === "string" && !!item) : [];
  const result = fromInteraction({ ...payload, id: first.id, kind: payload.kind || "clarification", question: typeof payload.question === "string" && payload.question.trim() ? payload.question.trim() : "请补充研究范围", choices });
  // 旧版审核对象保留原载荷，中断身份只放在外层交互中。
  if (result.researchPlan) result.researchPlan = payload as ResearchPlanPrompt;
  if (result.researchCandidates) result.researchCandidates = payload as CandidateReviewPrompt;
  return result;
}
