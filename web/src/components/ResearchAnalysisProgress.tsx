import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api, errorText } from "../api/client";
import type { ResearchRun, ResearchWorkItem } from "../types";
import { analysisProgress } from "./researchAnalysis";

const stateLabels: Record<string, string> = { pending: "等待分析", reading: "正在阅读", completed: "已完成", failed: "分析失败" };
const fieldLabels: Record<string, string> = { method: "方法", datasets: "数据集", metrics: "评测指标", code: "代码", reproduction: "复现条件" };
const certaintyLabels: Record<string, string> = { finding: "有原文依据", inference: "推断", unverified: "待验证", unknown: "未知" };

export function SessionResearchAnalysis({ projectID, turnID, onChanged }: { projectID: string; turnID: string; onChanged: () => void }) {
  const [run, setRun] = useState<ResearchRun | null>(null);
  useEffect(() => {
    let disposed = false;
    const refresh = async () => {
      try { const result = await api.researchRuns(projectID); if (!disposed) setRun(result.items.find((item) => item.turn_id === turnID) || null); }
      catch { /* 会话本身的连接状态由外层提示，下一次轮询继续同步任务进度。 */ }
    };
    setRun(null); void refresh();
    const timer = window.setInterval(() => void refresh(), 1500);
    return () => { disposed = true; window.clearInterval(timer); };
  }, [projectID, turnID]);
  return run ? <ResearchAnalysisProgress projectID={projectID} run={run} onChanged={onChanged} /> : null;
}

export function ResearchAnalysisProgress({ projectID, run, onChanged }: { projectID: string; run: ResearchRun; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const pending = useRef<{ key: string; operation: string; revision: number } | null>(null);
  const items = run.work_items || [];
  const progress = analysisProgress(items);
  if (!items.length) return null;
  const paused = run.pause_requested && run.execution_state === "awaiting_input";
  const active = run.execution_state === "running" || run.execution_state === "queued";
  async function control(kind: "pause" | "continue" | "retry" | "cancel", item?: ResearchWorkItem) {
    if (busy) return;
    setBusy(true); setError("");
    try {
      const key = `${run.id}:${kind}:${item?.id || ""}:${item?.revision || 0}`;
      if (pending.current?.key !== key) {
        const turn = await api.turn(projectID, run.session_id, run.turn_id);
        pending.current = { key, operation: crypto.randomUUID(), revision: turn.revision };
      }
      await api.controlResearch(projectID, run.id, kind, {
        revision: pending.current.revision, idempotency_key: pending.current.operation,
        ...(item ? { paper_id: item.paper_id, work_revision: item.revision } : {}),
      });
      pending.current = null;
      onChanged();
    } catch (reason) {
      setError(errorText(reason));
      if (reason instanceof Error && "status" in reason && (reason as { status?: number }).status === 409) { pending.current = null; onChanged(); }
    }
    finally { setBusy(false); }
  }
  return <section className="research-analysis" aria-label="逐篇论文分析">
    <div className="section-heading"><h3>逐篇分析</h3><span className="tag">{run.execution_state === "cancelled" || run.phase === "cancelled" ? "已取消" : paused ? "已暂停" : run.pause_requested ? "等待安全边界暂停" : ["analyses_completed", "reporting", "reported"].includes(run.phase) ? "逐篇处理结束" : "分析中"}</span></div>
    <p role="status">已完成 {progress.completed}/{progress.total} 篇{progress.failed ? ` · 失败 ${progress.failed} 篇` : ""}{progress.current && !paused ? ` · 正在阅读：${progress.current}` : ""}</p>
    <progress aria-label="论文处理进度" max={progress.total} value={progress.completed + progress.failed} />
    <p className="muted">每篇结果独立保存，刷新不会丢失。暂停会等待当前请求结束；继续和重试沿用累计预算。新任务的矩阵与报告见下方；旧版任务保留原有逐篇成果。</p>
    <div className="research-controls">
      {active && !run.pause_requested && ["analyzing", "reporting"].includes(run.phase) && <button className="button secondary" disabled={busy} onClick={() => void control("pause")}>暂停调研</button>}
      {paused && <button className="button primary" disabled={busy} onClick={() => void control("continue")}>继续调研</button>}
      {(active || paused) && <button className="button secondary" disabled={busy} onClick={() => void control("cancel")}>取消调研</button>}
    </div>
    {error && <p role="alert" className="action-error">{error}</p>}
    <div className="research-analysis-items">{items.map((item) => <details className="research-analysis-item" key={item.id}>
      <summary><span>{item.title}</span><span className="tag">{stateLabels[item.state] || item.state}</span></summary>
      <p><Link to={`/p/${projectID}/papers?paper=${item.paper_id}`}>打开论文</Link> · {item.content_kind === "abstract" ? "仅阅读摘要" : item.document_id ? "已冻结全文文档" : "尚无可用文档"} · 尝试 {item.attempts} 次</p>
      {item.analysis?.summary && <p>{item.analysis.summary}</p>}
      {item.analysis?.error && <p className="action-error">失败原因：{item.analysis.error}</p>}
      {item.analysis?.fields?.map((field) => <div className="research-analysis-field" key={field.name}>
        <strong>{fieldLabels[field.name] || field.name}</strong><span className="tag">{certaintyLabels[field.status] || field.status}</span><p>{field.text}</p>
        {field.evidence_ids?.map((id, index) => <Link key={id} className="text-button" to={`/p/${projectID}/papers?paper=${item.paper_id}&evidence=${id}`}>查看原文引用 {index + 1}</Link>)}
      </div>)}
      {item.state === "failed" && !active && run.execution_state !== "awaiting_input" && run.execution_state !== "cancelled" && <button className="button secondary" disabled={busy} onClick={() => void control("retry", item)}>重试此论文</button>}
    </details>)}</div>
  </section>;
}
