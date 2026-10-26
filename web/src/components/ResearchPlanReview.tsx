import { useEffect, useRef, useState } from "react";
import type { ResearchPlan, ResearchPlanPrompt, Turn, UITarget } from "../types";
import { api, errorText } from "../api/client";

export function ResearchPlanReview({ projectID, target, turn, interruptID, prompt, onSubmitted }: {
  projectID: string; target: UITarget; turn: Turn; interruptID: string; prompt: ResearchPlanPrompt; onSubmitted: () => Promise<void>;
}) {
  const [plan, setPlan] = useState<ResearchPlan>(prompt.plan);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const operation = useRef({ fingerprint: "", id: "" });
  useEffect(() => { setPlan(prompt.plan); setError(""); operation.current = { fingerprint: "", id: "" }; }, [interruptID, prompt.hash]);
  async function submit(decision: "approve" | "cancel") {
    if (busy) return;
    const cleanedPlan = { ...plan, queries: plan.queries.map((value) => value.trim()).filter(Boolean), inclusion: plan.inclusion?.map((value) => value.trim()).filter(Boolean), exclusion: plan.exclusion?.map((value) => value.trim()).filter(Boolean) };
    const review = { run_id: prompt.run_id, plan_revision: prompt.revision, plan_hash: prompt.hash, decision, plan: cleanedPlan };
    const fingerprint = JSON.stringify({ revision: turn.revision, review });
    if (operation.current.fingerprint !== fingerprint) operation.current = { fingerprint, id: crypto.randomUUID() };
    setBusy(true); setError("");
    try { await api.respondToInteraction(projectID, target, turn, [{ interrupt_id: interruptID, research_plan: review }], operation.current.id); await onSubmitted(); }
    catch (reason) { setError(errorText(reason)); }
    finally { setBusy(false); }
  }
  const lines = (value: string) => value.split("\n");
  return <section className="question-card research-plan-review" aria-label="研究计划审核">
    <strong>确认研究计划 · v{prompt.revision}</strong><p>{prompt.question}</p>
    <p className="muted">确认后助手才会按此计划检索；可以先修改内容，也可以取消任务。</p>
    <fieldset disabled={busy} className="direction-form">
      <label>计划说明<textarea aria-label="计划说明" maxLength={4000} value={plan.summary} onChange={(event) => setPlan({ ...plan, summary: event.target.value })} /></label>
      <label>检索式（每行一条）<textarea aria-label="检索式" value={plan.queries.join("\n")} onChange={(event) => setPlan({ ...plan, queries: event.target.value.split("\n") })} /></label>
      <label>纳入条件<textarea aria-label="纳入条件" value={(plan.inclusion || []).join("\n")} onChange={(event) => setPlan({ ...plan, inclusion: lines(event.target.value) })} /></label>
      <label>排除条件<textarea aria-label="排除条件" value={(plan.exclusion || []).join("\n")} onChange={(event) => setPlan({ ...plan, exclusion: lines(event.target.value) })} /></label>
    </fieldset>
    {error && <p role="alert" className="action-error">{error}</p>}
    <div className="paper-result-actions"><button className="button primary" disabled={busy || !plan.summary.trim() || !plan.queries.some((value) => value.trim())} onClick={() => void submit("approve")}>确认计划并继续</button><button className="button secondary" disabled={busy} onClick={() => void submit("cancel")}>取消本次调研</button></div>
  </section>;
}
