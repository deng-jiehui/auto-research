import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, errorText } from "../api/client";
import type { ReportContext, ReportReference } from "../types";

export function ReportContextCard({ projectID, context, disabled, onChoose, onRemove }: {
  projectID: string; context: ReportContext; disabled: boolean; onChoose: (id: string) => void; onRemove: () => void;
}) {
  const [versions, setVersions] = useState<ReportReference[]>([]);
  const [error, setError] = useState("");
  const [reload, setReload] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    let timer = 0;
    setVersions([]); setError("");
    async function refresh() {
      try {
        const result = await api.researchReports(projectID, context.run_id, controller.signal);
        if (!controller.signal.aborted) { setVersions(result.items); setError(""); }
      } catch (reason) { if (!controller.signal.aborted) setError(errorText(reason)); }
      finally { if (!controller.signal.aborted) timer = window.setTimeout(() => void refresh(), 15000); }
    }
    void refresh();
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [projectID, context.run_id, reload]);
  const latest = versions.reduce((best, item) => item.revision > (best?.revision || 0) ? item : best, undefined as ReportReference | undefined);
  return <section className="report-context-card" aria-label="当前引用的研究报告">
    <div className="section-heading"><div><small>正在围绕报告讨论</small><strong>{context.title}</strong></div><button className="text-button" disabled={disabled} onClick={onRemove}>移除报告引用</button></div>
    <div className="report-context-controls">
      <label>讨论版本 <select aria-label="讨论报告版本" value={context.report_id} disabled={disabled} onChange={(event) => onChoose(event.target.value)}>
        {!versions.some((item) => item.id === context.report_id) && <option value={context.report_id}>v{context.revision}</option>}
        {versions.map((item) => <option key={item.id} value={item.id}>v{item.revision}{item.id === latest?.id ? " · 最新" : ""}</option>)}
      </select></label>
      <Link className="text-button" to={context.report_url}>打开引用报告</Link>
      <button className="text-button" disabled={disabled} onClick={() => setReload((value) => value + 1)}>检查新版本</button>
    </div>
    {latest && latest.revision > context.revision && <p className="report-context-update" role="status">已有 v{latest.revision}，当前仍讨论 v{context.revision}。<button className="text-button" disabled={disabled} onClick={() => onChoose(latest.id)}>切换到新版</button></p>}
    {error && <p className="action-error">报告版本检查失败：{error}；当前引用仍保留。</p>}
    <details><summary>报告摘要与使用方式</summary><p>{context.summary}</p><p className="muted">助手按需读取这份报告及其原文证据。后续消息沿用此版本，移除或切换将在下一条消息生效。</p></details>
  </section>;
}
