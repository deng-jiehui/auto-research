import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { api, errorText } from "./api/client";
import type { ReportContext, ReportSelection, Turn } from "./types";

// 选择草稿属于当前输入；发送后从服务端轮次恢复，URL 只负责打开入口。
export function useReportContext(projectID: string, turn?: Turn) {
  const [params, setParams] = useSearchParams();
  const requested = params.get("report");
  const [draft, setDraft] = useState<{ value: ReportContext | null; request: ReportSelection }>();
  const [processed, setProcessed] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [reload, setReload] = useState(0);
  useEffect(() => {
    if (!requested) return;
    const controller = new AbortController();
    setPending(true); setError("");
    void api.researchReport(projectID, requested, controller.signal).then((report) => {
      if (controller.signal.aborted) return;
      const root = `research://reports/${report.id}/`;
      setDraft({ request: { run_id: report.run_id, report_id: report.id }, value: {
        run_id: report.run_id, report_id: report.id, revision: report.revision, title: report.title,
        summary: report.summary, matrix_hash: report.matrix_hash, resource_path: report.resource_path,
        matrix_path: `${root}matrix.json`, references_path: `${root}references.json`,
        report_url: `/p/${encodeURIComponent(projectID)}/research/${encodeURIComponent(report.run_id)}?report=${encodeURIComponent(report.id)}`,
      } });
    }).catch((reason) => { if (!controller.signal.aborted) setError(`报告引用无法载入：${errorText(reason)}`); })
      .finally(() => { if (!controller.signal.aborted) { setPending(false); setProcessed(requested); } });
    return () => controller.abort();
  }, [projectID, requested, reload]);

  function clearQuery() {
    setParams((current) => { const next = new URLSearchParams(current); next.delete("report"); return next; }, { replace: true });
  }
  return {
    context: draft ? draft.value : turn?.report_context || null,
    selection: draft?.request,
    loading: pending || !!requested && processed !== requested,
    error,
    retry: () => setReload((value) => value + 1),
    choose: (id: string) => setParams((current) => { const next = new URLSearchParams(current); next.set("report", id); return next; }, { replace: true }),
    remove: () => { setDraft({ value: null, request: { clear: true } }); setError(""); setPending(false); clearQuery(); },
    committed: () => { setDraft(undefined); setError(""); setPending(false); clearQuery(); },
  };
}
