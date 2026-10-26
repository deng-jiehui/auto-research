import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, errorText } from "../api/client";
import { ResearchReportPanel } from "../components/ResearchReportPanel";
import { ResearchAnalysisProgress } from "../components/ResearchAnalysisProgress";
import { UserInteraction } from "../components/UserInteraction";
import { pendingInteraction } from "../interaction";
import { useInteractionEvents } from "../useInteractionEvents";
import { MarkdownRenderer } from "../components/MarkdownRenderer";
import { RuntimeDetails } from "../components/RuntimeDetails";
import { canDeleteResearch, researchStateLabels } from "../components/ResearchTaskCards";
import { DeleteResearchDialog } from "../components/DeleteResearchDialog";
import { useNotice } from "../components/Notifications";
import type { ResearchRun, SessionSnapshot } from "../types";
import { streamingAssistantText, visibleMessages, visibleProgress } from "./SessionPage";

export function ResearchTaskPage() {
  const { projectId = "", runId = "" } = useParams();
  const navigate = useNavigate();
  const notice = useNotice();
  const [data, setData] = useState<{ run: ResearchRun; snapshot: SessionSnapshot } | null>(null);
  const [error, setError] = useState("");
  const [loadError, setLoadError] = useState("");
  const [busy, setBusy] = useState(false);
  const [runtimeOpen, setRuntimeOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const version = useRef(0);
  const operation = useRef<{ revision: number; kind: string; id: string } | null>(null);
  const refresh = useCallback(async () => {
    const current = ++version.current;
    try {
      const snapshot = await api.researchSnapshot(projectId, runId);
      const run = snapshot.research || await api.researchRun(projectId, runId);
      if (current === version.current) { setData({ run, snapshot }); setLoadError(""); }
    } catch (reason) { if (current === version.current) setLoadError(errorText(reason)); }
  }, [projectId, runId]);
  useEffect(() => {
    let disposed = false;
    let timer = 0;
    setData(null); setError(""); setLoadError(""); setDeleting(false); operation.current = null;
    const poll = async () => { await refresh(); if (!disposed) timer = window.setTimeout(() => void poll(), 1000); };
    void poll();
    return () => { disposed = true; version.current++; window.clearTimeout(timer); };
  }, [refresh]);
  const run = data?.run.id === runId ? data.run : undefined;
  const turn = run ? data?.snapshot.turn : undefined;
  useInteractionEvents(projectId, { kind: "research", id: runId }, turn?.state, refresh, data?.snapshot.ui?.event_cursor);
  const question = run ? pendingInteraction(data?.snapshot) : null;
  async function control(kind: "retry" | "cancel") {
    if (!run || !turn || busy) return;
    setBusy(true); setError("");
    try {
      if (operation.current?.kind !== kind || operation.current.revision !== turn.revision) operation.current = { kind, revision: turn.revision, id: crypto.randomUUID() };
      if (kind === "cancel") {
        await api.controlResearch(projectId, run.id, kind, { revision: turn.revision, idempotency_key: operation.current.id });
      } else {
        const result = await api.controlResearch(projectId, run.id, "retry", { revision: turn.revision, idempotency_key: operation.current.id });
        if (result.turn.id !== turn.id) {
          const runs = await api.researchRuns(projectId);
          const next = runs.items.find((item) => item.turn_id === result.turn.id);
          if (!next) throw new Error("新任务已创建，请在调研任务列表查看。");
          operation.current = null;
          navigate(`/p/${projectId}/research/${next.id}`);
          return;
        }
      }
      operation.current = null;
      await refresh();
    } catch (reason) { setError(errorText(reason)); }
    finally { setBusy(false); }
  }
  const active = ["queued", "running", "awaiting_input", "recovery_required"].includes(turn?.state || "");
  const deletable = canDeleteResearch(turn?.state);
  const progress = visibleProgress(data?.snapshot.events || []);
  const text = active ? streamingAssistantText(data?.snapshot.events || []) : "";
  const messages = visibleMessages(data?.snapshot.messages || []).filter((message) => message.role === "assistant");
  return <main className="papers-page research-task-page">
    <header className="papers-header">
      <Link className="text-button" to={`/p/${projectId}/research`}>← 调研任务列表</Link>
      <span className="eyebrow">RESEARCH AGENT</span><h1>{run?.spec.question || "调研任务"}</h1>
      <p>研究独立执行，计划、论文选择和补充信息在这里处理。会话同步简要结果，仍可继续讨论。</p>
      {turn && <div className="research-controls"><span className="tag research-task-status" data-state={turn.state}>{researchStateLabels[turn.state] || turn.state}</span>
        {run?.origin_session_id && <Link className="button secondary" to={`/p/${projectId}/s/${run.origin_session_id}`}>返回来源会话</Link>}
        {(["failed", "completed", "cancelled"].includes(turn.state)) && <button className="button secondary" disabled={busy} onClick={() => void control("retry")}>{turn.state === "completed" || turn.state === "cancelled" ? "重新调研" : "重试调研"}</button>}
        {active && <button className="button secondary" disabled={busy} onClick={() => void control("cancel")}>结束任务</button>}
        <button className="button secondary danger-text" disabled={busy || !deletable} title={active ? "结束任务后可删除" : "删除调研任务"} onClick={() => setDeleting(true)}>删除任务</button>
        {active && <span className="muted research-delete-hint">结束任务后可删除</span>}
        {(import.meta.env.DEV || new URLSearchParams(location.search).get("dev") === "1") && <button className="text-button" onClick={() => setRuntimeOpen(true)}>开发运行详情</button>}
      </div>}
    </header>
    {error && <p className="action-error" role="alert">{error}</p>}
    {loadError && <p className="action-error" role="alert">{loadError}</p>}
    {!data && !loadError && <p role="status">正在读取任务…</p>}
    {data && run && turn && <>
      <section className="card research-task-scope"><h2>任务范围</h2>
        <p>模型调用 {run.model_calls}/{run.spec.model_call_limit} · 工具执行 {run.tool_calls}/{run.spec.tool_call_limit} · Token {run.input_tokens + run.output_tokens}</p>
        {run.spec.constraints?.length ? <ul>{run.spec.constraints.map((value, i) => <li key={i}>{value}</li>)}</ul> : null}
        {run.spec.paper_ids?.length ? <p>指定起始论文：{run.spec.paper_ids.map((id, i) => <Link key={id} to={`/p/${projectId}/papers?paper=${id}`}>论文 {i + 1} </Link>)}</p> : null}
        <details><summary>成果位置与执行范围</summary><code>research://runs/{run.id}.json</code><p>候选上限 {run.spec.settings.candidate_limit} · 分析上限 {run.spec.settings.analyze_limit} · 来源 {run.spec.settings.providers.join("、")}</p><p>任务 ID：{run.id}</p>{run.previous_run_id && <Link to={`/p/${projectId}/research/${run.previous_run_id}`}>查看此前调研</Link>}</details>
      </section>
      <UserInteraction projectID={projectId} target={{ kind: "research", id: run.id }} turn={turn} question={question} onSubmitted={refresh} />
      <ResearchAnalysisProgress projectID={projectId} run={run} onChanged={() => void refresh()} />
      <ResearchReportPanel key={run.id} projectID={projectId} run={run} />
      {run.plan_revision > 0 && !question?.researchPlan && <details className="card"><summary>研究计划 v{run.plan_revision} · {run.approved_plan_revision === run.plan_revision ? "已确认" : "未确认"}</summary><p>{run.plan.summary}</p><ul>{run.plan.queries?.map((query, i) => <li key={i}>{query}</li>)}</ul></details>}
      {(turn.error_message || run.error_message) && <p className="action-error" role="alert">{turn.error_message || run.error_message}</p>}
      {turn.state === "recovery_required" && <p className="action-error">当前执行状态需要恢复，请先结束任务，再点击“重新调研”。已保存结果会保留。</p>}
      {progress.length > 0 && <details className="card"><summary>执行进度</summary>{progress.map((item) => <div key={item.id}><strong>{item.title}</strong><p>{item.summary}</p></div>)}</details>}
      {(messages.length > 0 || text) && <section className="card research-task-output" aria-label="调研执行说明"><h2>执行说明</h2>{messages.map((message) => <MarkdownRenderer key={message.record.id} source={message.text} />)}{text && <MarkdownRenderer source={text} />}</section>}
      {runtimeOpen && <RuntimeDetails events={data.snapshot.events} turn={turn} onClose={() => setRuntimeOpen(false)} />}
      {deleting && <DeleteResearchDialog projectID={projectId} run={run} onClose={() => setDeleting(false)} onDeleted={() => { setDeleting(false); notice("调研任务已删除"); navigate(`/p/${projectId}/research`, { replace: true }); }} />}
    </>}
  </main>;
}
