import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { api, errorText } from "../api/client";
import type { Direction, DirectionDraft, Paper, Project } from "../types";
import { EmptyState } from "../components/EmptyState";

const emptyDraft: DirectionDraft = { name: "", description: "", paper_ids: [], questions: [], gap: "", feasibility: "" };
const stateLabels = { proposed: "待确认", accepted: "已确认", rejected: "已拒绝", archived: "已归档" };

function draftFrom(item: Direction): DirectionDraft {
  return { name: item.name, description: item.description, paper_ids: item.paper_ids || [], questions: item.questions || [], gap: item.gap || "", feasibility: item.feasibility || "" };
}

export function DirectionsPage() {
  const { projectId = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const [project, setProject] = useState<Project | null>(null);
  const [items, setItems] = useState<Direction[]>([]);
  const [papers, setPapers] = useState<Paper[]>([]);
  const [selected, setSelected] = useState<Direction | null>(null);
  const [draft, setDraft] = useState<DirectionDraft>(emptyDraft);
  const [history, setHistory] = useState<Direction[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const selectedID = params.get("direction");
  const dirty = !selected || JSON.stringify(draft) !== JSON.stringify(draftFrom(selected));

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setProject(null);
    setItems([]);
    setError("");
    Promise.all([api.project(projectId, controller.signal), api.directions(projectId, controller.signal), api.papers(projectId, "", controller.signal)])
      .then(([current, directions, library]) => { setProject(current); setItems(directions.items); setPapers(library.items); })
      .catch((reason) => { if (!controller.signal.aborted) setError(errorText(reason)); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [projectId]);

  useEffect(() => {
    const item = items.find((value) => value.id === selectedID) || null;
    setSelected(item);
    setDraft(item ? draftFrom(item) : emptyDraft);
    setHistory([]);
    if (!item) return;
    const controller = new AbortController();
    api.directionHistory(projectId, item.id, controller.signal).then((value) => setHistory(value.items))
      .catch((reason) => { if (!controller.signal.aborted) setError(errorText(reason)); });
    return () => controller.abort();
  }, [items, selectedID, projectId]);

  function update(item: Direction) {
    setItems((current) => current.some((old) => old.id === item.id) ? current.map((old) => old.id === item.id ? item : old) : [item, ...current]);
    setParams({ direction: item.id });
  }

  async function save(event: FormEvent) {
    event.preventDefault();
    if (busy) return;
    setBusy(true); setError(""); setMessage("");
    try {
      const submitted = { ...draft, questions: draft.questions?.map((question) => question.trim()).filter(Boolean) };
      const item = selected ? await api.editDirection(projectId, selected.id, selected.revision, submitted) : await api.proposeDirection(projectId, submitted);
      update(item);
      setMessage(selected ? "修改已保存，之前的确认失效，请重新确认。" : "提案已保存，确认后才成为正式方向。");
    } catch (reason) { setError(errorText(reason)); }
    finally { setBusy(false); }
  }

  async function decide(action: "accept" | "reject" | "archive") {
    if (!selected || busy) return;
    setBusy(true); setError(""); setMessage("");
    try {
      const item = await api.decideDirection(projectId, selected.id, selected.revision, action);
      update(item);
      setMessage(action === "accept" ? "方向已确认。" : action === "reject" ? "方向已拒绝，可编辑后重新提交。" : "方向已归档。");
    } catch (reason) { setError(errorText(reason)); }
    finally { setBusy(false); }
  }

  async function continueResearch() {
    if (!selected || selected.state !== "accepted" || busy) return;
    setBusy(true); setError("");
    try {
      const session = await api.createSession(projectId);
      const query = new URLSearchParams({ direction: selected.id, revision: String(selected.revision) });
      navigate(`/p/${projectId}/s/${session.id}?${query}`);
    } catch (reason) { setError(errorText(reason)); setBusy(false); }
  }

  async function startResearch() {
    if (!selected || selected.state !== "accepted" || busy) return;
    setBusy(true); setError("");
    try { const result = await api.createResearchTask(projectId, { question: selected.name, direction_id: selected.id, direction_revision: selected.revision }, crypto.randomUUID()); navigate(`/p/${projectId}/research/${result.run.id}`); }
    catch (reason) { setError(errorText(reason)); setBusy(false); }
  }
  if (loading) return <main className="standalone"><EmptyState loading title="正在打开研究方向" description="读取项目提案与版本…" /></main>;
  if (!project) return <main className="standalone"><EmptyState title="研究方向不可用" description={error || "项目不存在"} home /></main>;
  return <main className="papers-page directions-page">
    <header className="papers-header"><Link className="text-button" to={`/p/${projectId}`}>← 返回项目</Link><span className="eyebrow">RESEARCH DIRECTIONS</span><h1>{project.title} · 研究方向</h1><p>把讨论整理为可编辑提案，用户确认后再作为正式方向。研究空白仍需论文和实验验证。</p></header>
    {error && <p className="action-error" role="alert">{error}</p>}
    {message && <p className="direction-notice" role="status">{message}</p>}
    <div className="papers-layout">
      <section className="card paper-list"><div className="section-heading"><h2>方向 {items.length}</h2><button className="text-button" disabled={busy} onClick={() => { setParams({}); setMessage(""); }}>新建提案</button></div>
        {items.length === 0 && <p className="muted">让会话助手整理并保存方向，或在右侧手动新建。</p>}
        {items.map((item) => <button key={item.id} className={`paper-list-item ${selected?.id === item.id ? "active" : ""}`} disabled={busy} onClick={() => { setParams({ direction: item.id }); setMessage(""); setError(""); }}><strong>{item.name}</strong><small>{stateLabels[item.state]} · v{item.revision}</small></button>)}
      </section>
      <section className="card direction-detail">
        <h2>{selected ? `${stateLabels[selected.state]} · v${selected.revision}` : "新建研究方向"}</h2>
        {selected?.evidence_insufficient && <p className="direction-warning">依据不足：尚无代表论文</p>}
        <form className="direction-form" onSubmit={save}>
          <label>方向名称<input aria-label="方向名称" maxLength={120} value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} /></label>
          <label>方向说明<textarea aria-label="方向说明" maxLength={4000} value={draft.description} onChange={(event) => setDraft({ ...draft, description: event.target.value })} /></label>
          <label>关键问题（每行一个）<textarea aria-label="关键问题" value={(draft.questions || []).join("\n")} onChange={(event) => setDraft({ ...draft, questions: event.target.value.split("\n") })} /></label>
          <label>待验证空白或假设<textarea aria-label="待验证假设" maxLength={4000} value={draft.gap || ""} onChange={(event) => setDraft({ ...draft, gap: event.target.value })} /></label>
          <label>可行性<input aria-label="可行性" maxLength={4000} value={draft.feasibility || ""} onChange={(event) => setDraft({ ...draft, feasibility: event.target.value })} /></label>
          <fieldset><legend>代表论文</legend>{papers.length === 0 ? <p className="muted">论文库为空。可以先保存提案，稍后补充依据。</p> : papers.map((paper) => <label className="direction-paper" key={paper.id}><input type="checkbox" checked={draft.paper_ids?.includes(paper.id) || false} onChange={(event) => setDraft({ ...draft, paper_ids: event.target.checked ? [...(draft.paper_ids || []), paper.id] : draft.paper_ids?.filter((id) => id !== paper.id) })} />{paper.title}</label>)}</fieldset>
          <div className="paper-result-actions"><button className="button primary" disabled={busy || !dirty || !draft.name.trim() || !draft.description.trim()}>{selected ? "保存修改" : "保存提案"}</button></div>
        </form>
        {selected && <><div className="paper-result-actions">
          {selected.state === "proposed" && <><button className="button primary" disabled={busy || dirty} onClick={() => decide("accept")}>确认方向</button><button className="button secondary" disabled={busy || dirty} onClick={() => decide("reject")}>拒绝提案</button></>}
          {selected.state !== "archived" && <button className="text-button" disabled={busy || dirty} onClick={() => decide("archive")}>归档方向</button>}
          <button className="button secondary" disabled={busy || dirty || selected.state !== "accepted"} onClick={continueResearch}>继续研究</button>
<button className="button primary" disabled={busy || dirty || selected.state !== "accepted"} onClick={() => void startResearch()}>开始调研</button>
        </div><p className="muted">继续研究会新建会话并填入此方向的 ID 和版本，由你检查后发送；“开始调研”会登记任务、生成计划并等待你的审核。</p>
        <details className="direction-history"><summary>历史版本</summary>{history.map((item) => <p key={item.revision}>v{item.revision} · {stateLabels[item.state]} · {item.name}</p>)}</details>
        {selected.source_session_id && <Link className="text-button" to={`/p/${projectId}/s/${selected.source_session_id}`}>打开来源会话</Link>}</>}
      </section>
    </div>
  </main>;
}
