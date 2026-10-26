import { ResearchAnalysisProgress } from "../components/ResearchAnalysisProgress";
import { useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, errorText } from "../api/client";
import { DeleteResearchDialog } from "../components/DeleteResearchDialog";
import { canDeleteResearch } from "../components/ResearchTaskCards";
import { useNotice } from "../components/Notifications";
import type { Project, ResearchRun } from "../types";

export function ResearchRunsPage() {
  const { projectId = "" } = useParams();
  const navigate = useNavigate();
  const notice = useNotice();
  const pendingStart = useRef({ question: "", operationID: "" });
  const [project, setProject] = useState<Project | null>(null);
  const [runs, setRuns] = useState<ResearchRun[]>([]);
  const [refresh, setRefresh] = useState(0);
  const [question, setQuestion] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [deleting, setDeleting] = useState<ResearchRun | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    Promise.all([api.project(projectId, controller.signal), api.researchRuns(projectId, controller.signal)])
      .then(([value, result]) => { setProject(value); setRuns(result.items); })
      .catch((reason) => { if (!controller.signal.aborted) setError(errorText(reason)); });
    return () => controller.abort();
  }, [projectId, refresh]);
  useEffect(() => { const timer = window.setInterval(() => setRefresh((value) => value + 1), 1500); return () => window.clearInterval(timer); }, []);
  async function start() {
    if (!question.trim() || busy) return;
    setBusy(true); setError("");
    try {
      if (pendingStart.current.question !== question.trim() || !pendingStart.current.operationID) {
        pendingStart.current = { question: question.trim(), operationID: crypto.randomUUID() };
      }
      const pending = pendingStart.current;
      const result = await api.createResearchTask(projectId, { question: pending.question }, pending.operationID);
      navigate(`/p/${projectId}/research/${result.run.id}`);
    } catch (reason) { setError(errorText(reason)); setBusy(false); }
  }
  return <main className="papers-page research-runs-page">
    <header className="papers-header"><Link className="text-button" to={`/p/${projectId}`}>← 返回项目</Link><span className="eyebrow">RESEARCH TASKS</span><h1>{project?.title || "项目"} · 调研任务</h1><p>先由助手制定计划，经过你的编辑与确认后执行。确认计划与纳入论文后，助手逐篇阅读、保存带原文引用的分析；支持查看进度、暂停和失败单篇重试。新独立任务会自动综合证据矩阵和带引用报告，支持查看历史版本与导出 Markdown。</p></header>
    {error && <p className="action-error" role="alert">{error}</p>}
    <section className="card direction-form"><h2>新建调研</h2><label>调研问题<textarea aria-label="调研问题" maxLength={4000} value={question} onChange={(event) => setQuestion(event.target.value)} placeholder="例如：比较单卡条件下适合复现的时序预测基线" /></label><div><button className="button primary" disabled={!project || busy || !question.trim()} onClick={() => void start()}>开始调研</button></div></section>
    <div className="section-heading"><h2>已有任务</h2><button className="text-button" onClick={() => setRefresh((value) => value + 1)}>刷新调研任务</button></div>
    <div className="research-run-list">{runs.map((run) => <article className="card" key={run.id} id={run.id}>
      <div className="section-heading"><h2>{run.spec.question}</h2><span className="tag">{run.phase === "cancelled" || run.execution_state === "cancelled" ? "已取消" : run.execution_state === "failed" || run.execution_state === "recovery_required" ? "执行失败" : run.phase === "reported" ? "报告已保存" : run.phase === "reporting" ? "正在综合报告" : run.phase === "analyses_completed" ? (run.work_items?.some((item) => item.state === "failed") ? "逐篇处理结束（含失败）" : "逐篇分析完成") : run.pause_requested && run.execution_state === "awaiting_input" ? "已暂停" : run.work_items?.length ? "正在逐篇分析" : run.approved_candidate_revision ? "候选已确认" : run.phase === "candidate_review" ? "等待候选审核" : run.approved_plan_revision ? "正在检索候选" : "等待计划确认"}</span></div>
      <p>计划 v{run.plan_revision} · {run.approved_plan_revision === run.plan_revision && run.plan_revision > 0 ? "已确认" : "未确认"}</p>
      {run.plan.summary && <p>{run.plan.summary}</p>}
      {run.plan.queries?.length > 0 && <ul>{run.plan.queries.map((query, index) => <li key={index}>{query}</li>)}</ul>}
      {run.candidate_revision > 0 && <p>候选第 {run.candidate_round} 轮 · v{run.candidate_revision} · 当前 {run.candidates?.length || 0} 篇{run.candidate_review?.decision === "approve" ? ` · 已纳入 ${run.candidate_review.choices.filter((item) => item.selected).length} 篇 · 分析上限 ${run.candidate_review.analyze_limit}` : ""}</p>}
      {run.candidate_review && <details><summary>最近一次候选审核</summary><p>{run.candidate_review.decision === "approve" ? "用户已确认纳入集" : run.candidate_review.decision === "search_again" ? "全部排除，已请求重新检索" : "用户取消"} · 审核版本 v{run.candidate_review.candidate_revision}</p>{run.candidate_review.feedback && <p>补充要求：{run.candidate_review.feedback}</p>}<ul>{run.candidate_review.choices.map((choice) => <li key={choice.candidate_id}>{choice.selected ? "纳入" : "排除"} · {run.candidates?.find((item) => item.id === choice.candidate_id)?.title || choice.candidate_id}：{choice.reason}{choice.paper_id && <> · <Link to={`/p/${projectId}/papers?paper=${choice.paper_id}`}>查看论文</Link></>}</li>)}</ul></details>}
      <ResearchAnalysisProgress projectID={projectId} run={run} onChanged={() => setRefresh((value) => value + 1)} />
      {run.error_message && <p className="action-error">{run.error_message}</p>}
      <p className="muted">模型调用 {run.model_calls}/{run.spec.model_call_limit} · 工具执行 {run.tool_calls}/{run.spec.tool_call_limit} · Token {run.input_tokens + run.output_tokens}{run.unknown_usage > 0 ? `（另有 ${run.unknown_usage} 次用量未知）` : ""}</p>
      <details><summary>任务范围与执行记录</summary>{run.previous_run_id && <p>派生自 <a href={`#${run.previous_run_id}`}>此前调研任务</a>，旧成果仍保留。</p>}<p>执行尝试：{run.execution_attempt} · 创建时项目：{run.project_snapshot.title} · 项目版本 {run.project_snapshot.revision}</p><p>来源：{run.spec.settings.providers.join("、")} · 候选上限 {run.spec.settings.candidate_limit} · 分析上限 {run.spec.settings.analyze_limit}</p><p className="resource-identity">任务 ID：{run.id}<br />关联执行 Turn：{run.turn_id}<br />范围指纹：{run.spec_hash}</p></details>
      <div className="research-run-actions">
        <Link className="button secondary" to={run.detached ? `/p/${projectId}/research/${run.id}` : `/p/${projectId}/s/${run.session_id}`}>{run.detached ? "打开调研任务" : "打开执行会话"}</Link>
        <button className="button secondary danger-text" disabled={!canDeleteResearch(run.execution_state)} title={!canDeleteResearch(run.execution_state) ? "结束任务后可删除" : "删除调研任务"} onClick={() => setDeleting(run)}>删除任务</button>
        {!canDeleteResearch(run.execution_state) && <span className="muted research-delete-hint">结束任务后可删除</span>}
      </div>
    </article>)}</div>
    {deleting && <DeleteResearchDialog key={deleting.id} projectID={projectId} run={deleting} onClose={() => setDeleting(null)} onDeleted={() => { setRuns((items) => items.filter((run) => run.id !== deleting.id)); setDeleting(null); setRefresh((value) => value + 1); notice("调研任务已删除"); }} />}
  </main>;
}
