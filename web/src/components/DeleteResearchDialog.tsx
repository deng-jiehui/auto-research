import { useState } from "react";
import type { FormEvent } from "react";
import { api, errorText } from "../api/client";
import type { ResearchRun } from "../types";
import { Modal } from "./Modal";

export function DeleteResearchDialog({ projectID, run, onClose, onDeleted }: {
  projectID: string;
  run: ResearchRun;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function remove(event: FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true); setError("");
    try {
      await api.deleteResearch(projectID, run.id);
      onDeleted();
    } catch (reason) {
      setError(errorText(reason));
    } finally {
      setBusy(false);
    }
  }
  return <Modal title="删除调研任务" description="任务及其计划、候选、分析和报告将永久删除，无法撤销。" busy={busy} onClose={onClose}>
    <form onSubmit={remove}>
      <div className="modal-body">
        <p className="delete-warning">确认删除 <strong>{run.spec.question}</strong>？</p>
        <p>论文库中的论文和资料会保留。{run.detached ? "该任务的执行记录也会删除，来源会话保留。" : "普通会话的聊天记录会保留。"}</p>
        {error && <p className="form-error" role="alert">{error}</p>}
      </div>
      <footer className="modal-footer">
        <button type="button" className="button secondary" disabled={busy} onClick={onClose}>取消</button>
        <button type="submit" className="button danger" disabled={busy}>{busy ? "正在删除…" : "确认永久删除"}</button>
      </footer>
    </form>
  </Modal>;
}
