import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, errorText } from "../api/client";
import type { ResearchTaskSummary } from "../types";

export const researchStateLabels: Record<string, string> = {
  queued: "等待执行", running: "研究中", awaiting_input: "等待处理",
  completed: "已完成", failed: "执行失败", recovery_required: "需要恢复", cancelled: "已取消",
};

export const canDeleteResearch = (state?: string) => ["completed", "failed", "cancelled", "superseded"].includes(state || "");

export function ResearchTaskCards({ projectID, sessionID }: { projectID: string; sessionID: string }) {
  const [items, setItems] = useState<ResearchTaskSummary[]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    let timer = 0;
    setItems([]); setError("");
    const refresh = async () => {
      try {
        const result = await api.sessionResearch(projectID, sessionID, controller.signal);
        if (!controller.signal.aborted) { setItems(result.items); setError(""); }
      } catch (reason) {
        if (!controller.signal.aborted) setError(errorText(reason));
      } finally {
        if (!controller.signal.aborted) timer = window.setTimeout(() => void refresh(), 1500);
      }
    };
    void refresh();
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [projectID, sessionID]);
  if (!items.length && !error) return null;
  return <section className="research-task-cards" aria-label="独立调研任务">
    {error && <p className="action-error" role="alert">调研状态同步失败：{error}</p>}
    {items.map((item) => <article className="card research-task-card" key={item.id}>
      <div className="section-heading"><strong>{item.question}</strong><span className="tag">{item.state === "completed" && item.report_path ? "报告已保存" : researchStateLabels[item.state] || item.state}</span></div>
      <p>{item.summary}</p>
      <Link className="button secondary" to={item.task_url}>打开调研任务</Link>
      <details><summary>成果查询地址</summary><p>完整结果可在任务页查看，后续对话也可以让助手按此地址读取。</p><code>{item.resource_path}</code>{item.report_path && <><p>报告正文可按需读取：</p><code>{item.report_path}</code></>}</details>
    </article>)}
  </section>;
}
