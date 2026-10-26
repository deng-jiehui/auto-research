import { useEffect, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api, APIError, errorText } from "../api/client";
import type { EvidenceMatrix, ReportReference, ResearchReport, ResearchRun } from "../types";
import { MarkdownRenderer } from "./MarkdownRenderer";

const fields = [["method", "方法"], ["datasets", "数据集"], ["metrics", "评测指标"], ["code", "代码"], ["reproduction", "复现条件"]];
const certainty: Record<string, string> = { finding: "有原文依据", inference: "推断", unverified: "待验证", unknown: "未知" };
const states: Record<string, string> = { pending: "等待分析", reading: "正在阅读", completed: "已完成", failed: "分析失败" };

export function ResearchReportPanel({ projectID, run }: { projectID: string; run: ResearchRun }) {
  const [matrix, setMatrix] = useState<EvidenceMatrix | null>(null);
  const [reports, setReports] = useState<ReportReference[]>([]);
  const [selected, setSelected] = useState("");
  const [report, setReport] = useState<ResearchReport | null>(null);
  const [error, setError] = useState("");
  const [reportError, setReportError] = useState("");
  const [reload, setReload] = useState(0);
  const [opening, setOpening] = useState(false);
  const [discussionError, setDiscussionError] = useState("");
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const requestedReport = params.get("report");
  // 执行统计更新不重取报告正文；仅分析内容、报告身份改变时刷新目录。
  const signature = JSON.stringify((run.work_items || []).map((w) => [w.id, w.state, w.analysis_revision, w.analysis_hash, w.document_id]));
  useEffect(() => {
    let disposed = false;
    void Promise.all([api.researchMatrix(projectID, run.id), api.researchReports(projectID, run.id)])
      .then(([next, versions]) => {
        if (disposed) return;
        setMatrix(next); setReports(versions.items); setError("");
        if (requestedReport && !versions.items.some((item) => item.id === requestedReport)) {
          setSelected(""); setError("引用的报告不属于此任务或已不可用，请重新选择报告版本。");
        } else {
          setSelected((previous) => requestedReport || (versions.items.some((item) => item.id === previous) ? previous : versions.items[0]?.id || ""));
        }
      }).catch((reason) => { if (!disposed) setError(errorText(reason)); });
    return () => { disposed = true; };
  }, [projectID, run.id, signature, run.latest_report?.id, reload, requestedReport]);
  useEffect(() => {
    let disposed = false;
    setReport(null); setReportError("");
    if (selected) void api.researchReport(projectID, selected)
      .then((next) => { if (!disposed) setReport(next); })
      .catch((reason) => { if (!disposed) setReportError(errorText(reason)); });
    return () => { disposed = true; };
  }, [projectID, selected, reload]);
  function download() {
    if (!report) return;
    const url = URL.createObjectURL(new Blob([report.markdown], { type: "text/markdown;charset=utf-8" }));
    const anchor = document.createElement("a");
    anchor.href = url; anchor.download = `research-${run.id}-v${report.revision}.md`; anchor.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  async function discuss() {
    if (!report || opening) return;
    setOpening(true); setDiscussionError("");
    try {
      let sessionID = run.origin_session_id;
      if (sessionID) {
        try {
          const source = await api.session(projectID, sessionID);
          if (source.archived || source.kind === "research") sessionID = undefined;
        } catch (reason) {
          if (reason instanceof APIError && reason.status === 404) sessionID = undefined;
          else throw reason;
        }
      }
      if (!sessionID) sessionID = (await api.createSession(projectID)).id;
      navigate(`/p/${encodeURIComponent(projectID)}/s/${encodeURIComponent(sessionID)}?report=${encodeURIComponent(report.id)}`);
    } catch (reason) { setDiscussionError(errorText(reason)); }
    finally { setOpening(false); }
  }
  if (!run.work_items?.length) return null;
  return <section className="card research-report-panel" aria-label="证据矩阵与研究报告">
    <h2>证据矩阵与研究报告</h2>
    <p className="muted">矩阵汇总已纳入论文的分析，保留失败与未知项。报告综合比较、冲突、待验证空白和实验建议；每条事实可追溯原文，推断仍需核实。</p>
    {(error || reportError) && <p className="action-error" role="alert">{error || reportError} <button className="text-button" onClick={() => setReload((value) => value + 1)}>重新加载成果</button></p>}
    <details open className="research-matrix"><summary>证据矩阵 · {matrix?.rows.length || 0} 篇</summary>
      <div className="research-matrix-scroll" tabIndex={0} aria-label="横向滚动证据矩阵">
        <table><caption className="sr-only">当前纳入论文的五字段证据矩阵</caption><thead><tr><th scope="col">论文 / 阅读级别</th>{fields.map(([id, label]) => <th scope="col" key={id}>{label}</th>)}</tr></thead>
          <tbody>{matrix?.rows.map((row) => <tr key={row.work_item_id}>
            <th scope="row"><Link to={`/p/${projectID}/papers?paper=${row.paper_id}`}>{row.title}</Link><p><span className="tag">{states[row.state] || row.state}</span> <span className="tag">{row.content_kind === "abstract" ? "仅摘要" : row.document_id ? "冻结全文" : "未取得来源"}</span></p><small>分析 v{row.analysis_revision}</small></th>
            {fields.map(([id]) => { const field = row.analysis?.fields?.find((item) => item.name === id); return <td key={id}>
              {row.state === "failed" ? <span className="action-error">失败：{row.analysis?.error || "来源不可用"}</span> : field ? <><span className="tag">{certainty[field.status] || field.status}</span><p>{field.text}</p>{field.evidence_ids?.map((evidence, index) => <Link key={evidence} to={`/p/${projectID}/papers?paper=${row.paper_id}&evidence=${evidence}`}>引用 {index + 1} </Link>)}</> : "待分析 / 未知"}
            </td>; })}
          </tr>)}</tbody></table>
      </div>
    </details>
    <div className="section-heading"><h3>研究报告</h3>{reports.length > 0 && <label>报告版本 <select aria-label="报告版本" value={selected} onChange={(event) => { setSelected(event.target.value); setParams((current) => { const next = new URLSearchParams(current); next.set("report", event.target.value); return next; }, { replace: true }); }}>{requestedReport && !reports.some((item) => item.id === requestedReport) && <option value="">请选择报告</option>}{reports.map((version) => <option key={version.id} value={version.id}>v{version.revision}{version.matrix_hash === matrix?.hash ? " · 当前矩阵" : " · 历史矩阵"}</option>)}</select></label>}</div>
    {!selected && <p className="muted">{run.phase === "reporting" ? "逐篇分析已保存，Research Agent 正在综合报告。" : "报告尚未生成。新独立任务会在逐篇分析结束后自动综合并保存报告。"}</p>}
    {selected && !report && !reportError && <p role="status">正在读取报告…</p>}
    {report && <>
      {matrix && report.matrix_hash !== matrix.hash && <p className="research-report-history" role="status">此报告属于历史矩阵版本。当前分析已有变化，原报告及其引用仍保留，请等待新的报告版本。</p>}
      <div className="research-report-actions"><button className="button" disabled={opening} onClick={() => void discuss()}>{opening ? "正在打开讨论…" : "基于此报告提问"}</button><button className="button secondary" onClick={download}>导出 Markdown</button><small>不可变报告 v{report.revision} · {new Date(report.created_at).toLocaleString()}</small></div>
      {discussionError && <p className="action-error" role="alert">{discussionError}</p>}
      <details><summary>报告资料地址</summary><code>{report.resource_path}</code></details>
      <div className="research-report-body"><MarkdownRenderer source={report.markdown} /></div>
    </>}
  </section>;
}
