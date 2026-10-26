import { useEffect, useRef, useState } from "react";
import { api, APIError, errorText } from "../api/client";
import type { PendingQuestion } from "../interaction";
import type { Turn, UITarget } from "../types";
import { ResearchPlanReview } from "./ResearchPlanReview";
import { ResearchCandidateReview } from "./ResearchCandidateReview";

export function UserInteraction({ projectID, target, turn, question, onSubmitted }: {
  projectID: string; target: UITarget; turn: Turn; question: PendingQuestion | null; onSubmitted: () => Promise<void>;
}) {
  if (!question) return null;
  if (question.unsupported) return <section className="question-card" role="status"><strong>暂时无法处理此交互</strong><p>{question.question}</p><p className="muted">请刷新页面、更新客户端或查看运行详情。任务会保留等待状态。</p></section>;
  if (question.researchPlan) return <ResearchPlanReview projectID={projectID} target={target} turn={turn} interruptID={question.id} prompt={question.researchPlan} onSubmitted={onSubmitted} />;
  if (question.researchCandidates) return <ResearchCandidateReview projectID={projectID} target={target} turn={turn} interruptID={question.id} prompt={question.researchCandidates} onSubmitted={onSubmitted} />;
  if (question.researchPause) return null;
  return <Clarification key={question.id} projectID={projectID} target={target} turn={turn} question={question} onSubmitted={onSubmitted} />;
}

function Clarification({ projectID, target, turn, question, onSubmitted }: {
  projectID: string; target: UITarget; turn: Turn; question: PendingQuestion; onSubmitted: () => Promise<void>;
}) {
  const [answer, setAnswer] = useState("");
  const [choices, setChoices] = useState(question.choices);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const operation = useRef({ fingerprint: "", id: "" });
  useEffect(() => { setAnswer(""); setChoices(question.choices); setError(""); operation.current = { fingerprint: "", id: "" }; }, [question.id]);
  async function submit() {
    if (busy || !answer.trim()) return;
    const fingerprint = JSON.stringify({ turn: turn.id, revision: turn.revision, interrupt: question.id, text: answer.trim() });
    if (operation.current.fingerprint !== fingerprint) operation.current = { fingerprint, id: crypto.randomUUID() };
    setBusy(true); setError("");
    try { await api.respondToInteraction(projectID, target, turn, [{ interrupt_id: question.id, text: answer.trim() }], operation.current.id); await onSubmitted(); }
    catch (reason) { setError(errorText(reason)); if (reason instanceof APIError && reason.status === 409) await onSubmitted(); }
    finally { setBusy(false); }
  }
  return <section className="question-card" aria-label={target.kind === "research" ? "调研补充信息" : "会话补充信息"}>
    <strong>需要你补充</strong><p>{question.question}</p>
    <fieldset disabled={busy}>
      {choices.length > 0 && <div className="question-choice-grid" aria-label="快捷选项">{choices.map((value, i) => <label className={`question-choice${answer === value ? " selected" : ""}`} key={i}>
        <input type="radio" name={`question-${question.id}`} aria-label={`选择选项 ${i + 1}`} checked={answer === value} onChange={() => setAnswer(value)} />
        <input type="text" aria-label={`编辑选项 ${i + 1}`} value={value} onChange={(event) => { const next = event.target.value; setChoices((items) => items.map((item, index) => index === i ? next : item)); if (answer === value || !answer) setAnswer(next); }} />
      </label>)}</div>}
      {question.allowCustom && <textarea aria-label="补充回答" value={answer} onChange={(event) => setAnswer(event.target.value)} placeholder={question.placeholder} rows={2} />}
      <button className="button primary" disabled={busy || !answer.trim()} onClick={() => void submit()}>提交回答</button>
    </fieldset>
    {error && <p className="action-error" role="alert">{error}</p>}
  </section>;
}
