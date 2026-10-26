import { useEffect, useRef, useState } from "react";
import { api, errorText, APIError } from "../api/client";
import type { CandidateChoice, CandidateReviewDecision, CandidateReviewPrompt, ResearchCandidate, Turn, UITarget } from "../types";

export function ResearchCandidateReview({ projectID, target, turn, interruptID, prompt, onSubmitted }: {
  projectID: string; target: UITarget; turn: Turn; interruptID: string; prompt: CandidateReviewPrompt; onSubmitted: () => Promise<void>;
}) {
  const [candidates, setCandidates] = useState<ResearchCandidate[]>([]);
  const [loading, setLoading] = useState(true);
  const [ready, setReady] = useState(false);
  const [reload, setReload] = useState(0);
  const initialChoices = (items = candidates) => items.map((item) => ({ candidate_id: item.id, selected: item.recommended, reason: item.recommendation_reason || "请填写纳入或排除理由" }));
  const [choices, setChoices] = useState<CandidateChoice[]>(initialChoices);
  const [limit, setLimit] = useState(prompt.analyze_limit);
  const [feedback, setFeedback] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const operation = useRef({ fingerprint: "", id: "" });
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true); setReady(false); setCandidates([]); setChoices([]); setLimit(prompt.analyze_limit); setFeedback(""); setError("");
    operation.current = { fingerprint: "", id: "" };
    api.researchRun(projectID, prompt.run_id, controller.signal).then((run) => {
      const items = run.candidates || [];
      if (run.candidate_revision !== prompt.revision || run.candidate_hash !== prompt.hash || JSON.stringify(items.map((item) => item.id)) !== JSON.stringify(prompt.candidate_ids || [])) throw new Error("候选集合已变化，请刷新当前审核。");
      setCandidates(items); setChoices(initialChoices(items)); setReady(true);
    }).catch((reason) => { if (!controller.signal.aborted) setError(errorText(reason)); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [projectID, interruptID, prompt.hash, reload]);
  const selected = choices.filter((item) => item.selected).length;
  const valid = Number.isInteger(limit) && limit >= 1 && limit <= prompt.analyze_limit && choices.every((item) => item.reason.trim());
  const update = (id: string, changes: Partial<CandidateChoice>) => setChoices((items) => items.map((item) => item.candidate_id === id ? { ...item, ...changes } : item));
  async function submit(decision: CandidateReviewDecision["decision"]) {
    if (busy || loading) return;
    const review: CandidateReviewDecision = { run_id: prompt.run_id, candidate_revision: prompt.revision, candidate_hash: prompt.hash, decision, analyze_limit: limit, choices: choices.map((item) => ({ ...item, reason: item.reason.trim() })), feedback: feedback.trim() };
    const fingerprint = JSON.stringify({ revision: turn.revision, review });
    if (operation.current.fingerprint !== fingerprint) operation.current = { fingerprint, id: crypto.randomUUID() };
    setBusy(true); setError("");
    try { await api.respondToInteraction(projectID, target, turn, [{ interrupt_id: interruptID, research_candidates: review }], operation.current.id); await onSubmitted(); }
    catch (reason) { setError(errorText(reason)); if (reason instanceof APIError && reason.status === 409) await onSubmitted(); }
    finally { setBusy(false); }
  }
  return <section className="question-card research-candidate-review" aria-label="候选论文审核">
    <div className="section-heading"><strong>确认候选论文 · v{prompt.revision}</strong><span className="tag">已选 {selected}/{candidates.length}</span></div>
    <p>{prompt.question}</p><p className="muted">摘要来自检索来源，推荐由助手生成。确认后只将勾选论文加入项目论文库，后续分析以此纳入集为范围。</p>
    {loading && <p className="muted">正在读取当前版本的候选论文…</p>}
    <fieldset disabled={busy || loading}>
      <div className="paper-result-actions"><button type="button" className="text-button" onClick={() => setChoices((items) => items.map((item) => ({ ...item, selected: false })))}>全部排除</button><button type="button" className="text-button" onClick={() => setChoices(initialChoices())}>恢复助手推荐</button></div>
      <div className="candidate-review-list">{candidates.map((candidate) => {
        const choice = choices.find((item) => item.candidate_id === candidate.id);
        return <article className={`candidate-review-item${choice?.selected ? " selected" : ""}`} key={candidate.id}>
          <label className="candidate-review-title"><input type="checkbox" aria-label={`纳入 ${candidate.title}`} checked={choice?.selected || false} onChange={(event) => update(candidate.id, { selected: event.target.checked })} /><strong>{candidate.title}</strong></label>
          <p className="muted">{candidate.year || "年份未知"} · {(candidate.authors || []).join("、") || "作者未知"} · {candidate.source_key}</p>
          <details><summary>查看摘要</summary><p className="candidate-abstract">{candidate.abstract || "来源未提供摘要，请在分析时核对。"}</p></details>
          <p className="muted">助手{candidate.recommended ? "建议纳入" : "建议排除"}：{candidate.recommendation_reason}</p>
          <label>{choice?.selected ? "纳入理由" : "排除理由"}<textarea aria-label={`${candidate.title} 的审核理由`} maxLength={1000} rows={2} value={choice?.reason || ""} onChange={(event) => update(candidate.id, { reason: event.target.value })} /></label>
        </article>;
      })}</div>
      {!loading && candidates.length === 0 && <p>本轮没有检索到可用候选。可以补充检索要求后重新检索，或取消任务。</p>}
      <label>本轮分析上限<input type="number" aria-label="本轮分析上限" min={1} max={prompt.analyze_limit} value={limit} onChange={(event) => setLimit(Number(event.target.value))} /></label><p className="muted">不超过创建任务时的上限 {prompt.analyze_limit} 篇；当前选择 {selected} 篇。</p>
      <label>补充要求<textarea aria-label="候选审核补充要求" maxLength={2000} rows={2} value={feedback} onChange={(event) => setFeedback(event.target.value)} placeholder="例如：优先公开代码和单卡可复现的论文；重新检索时助手会读取这些要求" /></label>
    </fieldset>
    {selected > limit && <p role="alert" className="action-error">已选论文超过分析上限，请减少选择或调整上限。</p>}
    {error && <p role="alert" className="action-error">{error}{!ready && <button className="text-button" onClick={() => { setReload((value) => value + 1); void onSubmitted(); }}>重新加载审核</button>}</p>}
    <div className="paper-result-actions"><button className="button primary" disabled={busy || loading || !ready || !valid || selected === 0 || selected > limit} onClick={() => void submit("approve")}>确认纳入并继续</button><button className="button secondary" disabled={busy || loading || !ready || !valid || selected > 0} onClick={() => void submit("search_again")}>全部排除并重新检索</button><button className="text-button" disabled={busy || loading} onClick={() => void submit("cancel")}>取消本次调研</button></div>
  </section>;
}
