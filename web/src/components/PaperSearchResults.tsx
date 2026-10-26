import { useRef, useState } from "react";
import { Link } from "react-router-dom";
import type { Paper, PaperCandidate, WorkspaceToolEvent } from "../types";
import { errorText } from "../api/client";

export function PaperSearchResults({ projectID, tools, papers, onAdd }: {
  projectID: string;
  tools: WorkspaceToolEvent[];
  papers: Paper[];
  onAdd: (paper: PaperCandidate) => Promise<void>;
}) {
  const [saving, setSaving] = useState<string[]>([]);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const pending = useRef(new Set<string>());
  const candidates = new Map<string, PaperCandidate>();
  for (const item of tools) {
    if (item.tool !== "search_papers" || item.is_error) continue;
    for (const paper of item.papers || []) {
      if (paper.source_key) candidates.set(paper.source_key, paper);
    }
  }
  if (candidates.size === 0) return null;

  async function add(candidate: PaperCandidate) {
    const key = candidate.source_key!;
    if (pending.current.has(key)) return;
    pending.current.add(key);
    setSaving((current) => [...current, key]);
    setErrors((current) => ({ ...current, [key]: "" }));
    try { await onAdd(candidate); }
    catch (reason) { setErrors((current) => ({ ...current, [key]: errorText(reason) })); }
    finally {
      pending.current.delete(key);
      setSaving((current) => current.filter((item) => item !== key));
    }
  }

  return <section className="paper-search-results" aria-label="Agent 检索到的论文">
    <div className="paper-results-heading"><strong>Agent 检索到的论文</strong><small>选择有用的资料加入项目，后续会话也可使用</small></div>
    {[...candidates.values()].map((candidate) => {
      const saved = papers.find((paper) => paper.source_key === candidate.source_key);
      const busy = saving.includes(candidate.source_key!);
      return <article className="paper-result-card" key={candidate.source_key}>
        <h3>{candidate.title}</h3>
        <small>{candidate.source_key}{candidate.year ? ` · ${candidate.year}` : ""}{candidate.authors?.length ? ` · ${candidate.authors.slice(0, 3).join("、")}` : ""}</small>
        {candidate.abstract && <details><summary>查看摘要</summary><p>{candidate.abstract}</p></details>}
        <div className="paper-result-actions">
          <button type="button" className="button secondary" disabled={!!saved || busy} onClick={() => void add(candidate)}>{saved ? "已加入论文库" : busy ? "正在加入…" : "加入论文库"}</button>
          {saved && <Link className="text-button" to={`/p/${projectID}/papers?paper=${encodeURIComponent(saved.id)}`}>查看论文</Link>}
        </div>
        {errors[candidate.source_key!] && <p className="form-error" role="alert">{errors[candidate.source_key!]}</p>}
      </article>;
    })}
  </section>;
}
