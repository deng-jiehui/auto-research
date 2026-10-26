import { SessionResearchAnalysis } from "../components/ResearchAnalysisProgress";
import { UserInteraction } from "../components/UserInteraction";
import { pendingInteraction } from "../interaction";
import { useInteractionEvents } from "../useInteractionEvents";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import type { MessageRecord, Paper, PaperCandidate, ProjectNote, ReportContext, ResearchProgress, Session, SessionEvent, SessionSnapshot, Turn, WorkspaceToolEvent } from "../types";
import { api, errorText } from "../api/client";
import { Icon } from "../components/Icon";
import { ProjectNotes } from "../components/ProjectNotes";
import { RenameSessionDialog } from "../components/RenameSessionDialog";
import { DeleteSessionDialog } from "../components/DeleteSessionDialog";
import { RuntimeDetails } from "../components/RuntimeDetails";
import { MarkdownRenderer } from "../components/MarkdownRenderer";
import { AgentModeSelector, useAgentMode } from "../components/AgentModeSelector";
import { PaperSearchResults } from "../components/PaperSearchResults";
import { MessageEditor } from "../components/MessageEditor";
import { ResearchTaskCards } from "../components/ResearchTaskCards";
import { ReportContextCard } from "../components/ReportContextCard";
import { useReportContext } from "../useReportContext";

export { noteSummary } from "../notes";
export { parsePendingQuestion } from "../interaction";

function parseJSONText(value: string): unknown {
  try {
    return JSON.parse(value);
  } catch {
    try {
      const binary = globalThis.atob(value);
      const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
      return JSON.parse(new TextDecoder().decode(bytes));
    } catch {
      return null;
    }
  }
}

export function messageText(record: MessageRecord) {
  try {
    const value = parseJSONText(record.content_json) as {
      role?: string;
      extra?: { report_context?: ReportContext };
      content_blocks?: Array<{ type?: string; text?: { text?: string } }>;
    };
    if (!value || typeof value !== "object") throw new Error("消息内容不是 JSON");
    return {
      role: value.role || "assistant",
      ...(value.extra?.report_context?.report_id && value.extra.report_context.run_id && value.extra.report_context.revision > 0 ? { report: value.extra.report_context } : {}),
      text: (value.content_blocks || [])
        .filter((block) => block.type === "text")
        .map((block) => block.text?.text || "")
        .join(""),
    };
  } catch {
    return { role: "assistant", text: "无法读取这条消息" };
  }
}

export function visibleMessages(records: MessageRecord[]) {
  const result: Array<ReturnType<typeof messageText> & { record: MessageRecord }> = [];
  for (const record of records) {
    const message = messageText(record);
    if (!message.text) continue;
    const previous = result[result.length - 1];
    if (message.role === "user" && previous?.role === "user" && previous.text === message.text && previous.report?.report_id === message.report?.report_id) {
      continue;
    }
    result.push({ ...message, record });
  }
  return result;
}

export function parseResearchProgress(event: Pick<SessionEvent, "kind" | "payload_json">): ResearchProgress | null {
  if (event.kind !== "research.progress") return null;
  const value = parseJSONText(event.payload_json) as Partial<ResearchProgress> | null;
  if (!value || typeof value !== "object") return null;
  if ([value.id, value.stage, value.status, value.title, value.summary].some((item) => typeof item !== "string" || !item.trim())) return null;
  return {
    id: value.id as string,
    stage: value.stage as string,
    status: value.status as string,
    title: value.title as string,
    summary: value.summary as string,
    facts: Array.isArray(value.facts) ? value.facts.filter((item): item is string => typeof item === "string") : undefined,
    next_actions: Array.isArray(value.next_actions) ? value.next_actions.filter((item): item is string => typeof item === "string") : undefined,
  };
}

export function visibleProgress(events: SessionEvent[]) {
  const latest = new Map<string, ResearchProgress>();
  for (const event of events) {
    const progress = parseResearchProgress(event);
    if (progress) latest.set(progress.id, progress);
  }
  return [...latest.values()];
}

export function parseWorkspaceToolEvent(event: Pick<SessionEvent, "kind" | "payload_json">): WorkspaceToolEvent | null {
  if (event.kind !== "workspace.tool" && event.kind !== "academic.tool") return null;
  const value = parseJSONText(event.payload_json) as Partial<WorkspaceToolEvent> | null;
  if (!value || typeof value !== "object") return null;
  if ([value.tool, value.call_id, value.status].some((item) => typeof item !== "string" || !item.trim())) return null;
  if (value.status !== "completed" && value.status !== "failed") return null;
  return {
    tool: value.tool as string,
    call_id: value.call_id as string,
    status: value.status,
    is_error: value.is_error === true,
    preview: typeof value.preview === "string" ? value.preview : undefined,
    papers: value.tool === "search_papers" && value.status === "completed" && !value.is_error && Array.isArray(value.papers)
      ? value.papers.filter((paper) => paper && typeof paper.title === "string" && typeof paper.source_key === "string" && /^(arxiv|doi):\S+$/.test(paper.source_key))
      : [],
    direction: value.tool === "submit_research_result" && !value.is_error && value.direction && typeof value.direction.id === "string" && typeof value.direction.name === "string" ? value.direction : undefined,
  };
}

export function visibleWorkspaceTools(events: SessionEvent[]) {
  const latest = new Map<string, WorkspaceToolEvent>();
  for (const event of events) {
    const toolEvent = parseWorkspaceToolEvent(event);
    if (toolEvent) latest.set(toolEvent.call_id, toolEvent);
  }
  return [...latest.values()];
}

export type AssistantBlock =
  | { kind: "heading"; text: string }
  | { kind: "paragraph"; text: string }
  | { kind: "unordered"; items: string[] }
  | { kind: "ordered"; items: string[] };

/** 将模型的 Markdown 摘要拆成可阅读的内容块，避免整段文字挤在一个 p 标签里。 */
export function assistantBlocks(text: string): AssistantBlock[] {
  const blocks: AssistantBlock[] = [];
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  let paragraph: string[] = [];
  let list: Extract<AssistantBlock, { kind: "unordered" | "ordered" }> | null = null;

  const flushParagraph = () => {
    if (paragraph.length > 0) {
      blocks.push({ kind: "paragraph", text: paragraph.join(" ").trim() });
      paragraph = [];
    }
  };
  const flushList = () => {
    if (list && list.items.length > 0) blocks.push(list);
    list = null;
  };

  for (const line of lines) {
    const value = line.trim();
    if (!value) {
      flushParagraph();
      flushList();
      continue;
    }
    const heading = value.match(/^#{1,3}\s+(.+)$/);
    if (heading) {
      flushParagraph();
      flushList();
      blocks.push({ kind: "heading", text: heading[1].trim() });
      continue;
    }
    const unordered = value.match(/^[-*]\s+(.+)$/);
    const ordered = value.match(/^\d+[.)]\s+(.+)$/);
    if (unordered || ordered) {
      flushParagraph();
      const kind = unordered ? "unordered" : "ordered";
      if (!list || list.kind !== kind) {
        flushList();
        list = kind === "unordered" ? { kind: "unordered", items: [] } : { kind: "ordered", items: [] };
      }
      if (list) list.items.push((unordered || ordered)?.[1].trim() || "");
      continue;
    }
    flushList();
    paragraph.push(value);
  }
  flushParagraph();
  flushList();
  return blocks;
}

export function streamingAssistantText(events: SessionEvent[]) {
  const messages = new Map<string, string>();
  let latestID = "";
  for (const event of events) {
    if (event.kind !== "assistant.delta") continue;
    const payload = parseJSONText(event.payload_json) as {
      message_id?: string;
      snapshot?: { id?: string; content_blocks?: Array<{ type?: string; text?: { text?: string } }> };
      delta?: { message_id?: string; op?: string; text?: string };
    } | null;
    if (!payload || typeof payload !== "object") continue;
    const id = payload.message_id || payload.delta?.message_id || payload.snapshot?.id;
    if (!id) continue;
    latestID = id;
    if (payload.snapshot) {
      messages.set(id, (payload.snapshot.content_blocks || []).filter((block) => block.type === "text").map((block) => block.text?.text || "").join(""));
    } else if (payload.delta?.op === "block_append") {
      messages.set(id, `${messages.get(id) || ""}${payload.delta.text || ""}`);
    }
  }
  return latestID ? messages.get(latestID) || "" : "";
}

function activeTurnState(state?: string) {
  return ["queued", "running", "awaiting_input", "recovery_required"].includes(state || "");
}

export function shouldSubscribeToEvents(state?: string) {
  return activeTurnState(state);
}

function AssistantContent({ text }: { text: string }) {
  return <MarkdownRenderer source={text} className="assistant-content" />;
}

export function turnFailureText(turn: Turn | undefined) {
  if (turn?.state !== "failed") return "";
  return turn.error_message || "本轮研究任务执行失败，请重试。";
}

export function turnFailureHint(turn: Turn | undefined) {
  const message = turnFailureText(turn).toLowerCase();
  if (message.includes("context deadline exceeded") || message.includes("deadline exceeded")) {
    return "模型响应超时，请稍后重试，或换用响应更快的模型。";
  }
  if (message.includes("项目笔记超过") || message.includes("笔记压缩")) {
    return "模型生成的笔记过长，系统已尝试压缩；请缩小研究范围后重试。";
  }
  return "请检查模型设置中的端点和模型名称后重试。";
}

export function SessionPage({ session, papers, onAddPaper, onPapersChanged, onSaved, onArchived, onDeleted }: { session: Session; papers: Paper[]; onAddPaper: (paper: PaperCandidate) => Promise<void>; onPapersChanged: () => void; onSaved: (session: Session) => void; onArchived: () => void; onDeleted: () => void }) {
  const agentMode = useAgentMode(session.project_id, session.id);
  const [renaming, setRenaming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [input, setInput] = useState("");
  const [params] = useSearchParams();
  const requestedDirection = params.get("direction");
  const requestedRevision = params.get("revision");
  useEffect(() => {
    if (!requestedDirection) return;
    const controller = new AbortController();
    api.direction(session.project_id, requestedDirection, controller.signal).then((item) => {
      if (String(item.revision) !== requestedRevision || item.state !== "accepted") throw new Error("方向版本已变化，请返回研究方向页重新确认。");
      setInput((current) => current || `继续研究方向“${item.name}”。先用 read_file 读取 research://directions/${item.id}.json（版本 ${item.revision}），帮我明确下一步研究问题。`);
    }).catch((reason) => { if (!controller.signal.aborted) setError(errorText(reason)); });
    return () => controller.abort();
  }, [session.project_id, requestedDirection, requestedRevision]);
  const [snapshot, setSnapshot] = useState<SessionSnapshot | null>(null);
  const reportBinding = useReportContext(session.project_id, snapshot?.turn);
  const [notes, setNotes] = useState<ProjectNote[]>([]);
  const [copiedMessage, setCopiedMessage] = useState<number | null>(null);
  const [runtimeOpen, setRuntimeOpen] = useState(false);
  const [editingMessage, setEditingMessage] = useState<{ recordID: string; turn: Turn; text: string } | null>(null);
  const [editError, setEditError] = useState("");
  const refreshVersion = useRef(0);
  const scrollArea = useRef<HTMLDivElement>(null);
  const followMessages = useRef(true);
  const devToolsVisible = import.meta.env.DEV || new URLSearchParams(globalThis.location?.search || "").get("dev") === "1";

  const refresh = useCallback(async () => {
    const version = ++refreshVersion.current;
    try {
      const value = await api.snapshot(session.project_id, session.id);
      if (version !== refreshVersion.current) return;
      setSnapshot(value);
      setNotes(value.notes);
      setError("");
    } catch (reason) {
      if (version !== refreshVersion.current) return;
      if (reason instanceof Error && "status" in reason && (reason as { status?: number }).status === 404) {
        setSnapshot(null);
        try {
          const value = await api.notes(session.project_id);
          if (version !== refreshVersion.current) return;
          setNotes(value.items);
          setError("");
        } catch (reason) {
          if (version !== refreshVersion.current) return;
          setError(errorText(reason));
        }
        return;
      }
      setError(errorText(reason));
    }
  }, [session.id, session.project_id]);

  const active = activeTurnState(snapshot?.turn.state);

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(), 1500);
    return () => window.clearInterval(timer);
  }, [refresh]);

  useInteractionEvents(session.project_id, { kind: "session", id: session.id }, snapshot?.turn.state, refresh, snapshot?.ui?.event_cursor);

  const messages = useMemo(() => visibleMessages(snapshot?.messages || []), [snapshot]);
  const progress = useMemo(() => snapshot ? visibleProgress(snapshot.events.filter((event) => event.turn_id === snapshot.turn.id)) : [], [snapshot]);
  const workspaceTools = useMemo(() => snapshot ? visibleWorkspaceTools(snapshot.events.filter((event) => event.turn_id === snapshot.turn.id)) : [], [snapshot]);
  const streamingText = useMemo(() => active && snapshot ? streamingAssistantText(snapshot.events.filter((event) => event.turn_id === snapshot.turn.id)) : "", [active, snapshot]);
  const question = pendingInteraction(snapshot);
  const latestEventID = snapshot?.events.at(-1)?.id;

  const savedPaperCallIDs = workspaceTools.filter((item) => item.tool === "save_paper" && !item.is_error).map((item) => item.call_id).join(",");
  useEffect(() => {
    if (savedPaperCallIDs) onPapersChanged();
  }, [savedPaperCallIDs, onPapersChanged]);

  useEffect(() => {
    if (scrollArea.current && followMessages.current) {
      scrollArea.current.scrollTop = scrollArea.current.scrollHeight;
    }
  }, [messages.length, streamingText, latestEventID, notes.length]);

  async function archive() {
    setBusy(true); setError("");
    try { await api.updateSession(session.project_id, session.id, { archived: !session.archived }); onArchived(); }
    catch (reason) { setError(errorText(reason)); }
    finally { setBusy(false); }
  }

  async function send() {
    const text = input.trim();
    if (!text || busy || active || editingMessage || agentMode.loading || agentMode.error || reportBinding.loading || reportBinding.error) return;
    setBusy(true); setError("");
    try {
      const result = await api.sendMessage(session.project_id, session.id, text, agentMode.mode, reportBinding.selection);
      refreshVersion.current++;
      setSnapshot((current) => ({ ...(current || { messages: [], notes: [], events: [] }), turn: result.turn }));
      setInput(""); reportBinding.committed(); await refresh();
    }
    catch (reason) { setError(errorText(reason)); }
    finally { setBusy(false); }
  }

  async function copyMessage(text: string, index: number) {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
      } else {
        const input = document.createElement("textarea");
        input.value = text;
        input.style.position = "fixed";
        input.style.opacity = "0";
        document.body.appendChild(input);
        input.select();
        if (!document.execCommand("copy")) throw new Error("复制命令未执行");
        input.remove();
      }
      setCopiedMessage(index);
      window.setTimeout(() => setCopiedMessage((current) => current === index ? null : current), 1600);
    } catch {
      setError("复制失败，请手动选择消息内容。");
    }
  }

  async function beginEdit(record: MessageRecord, text: string) {
    if (!record.turn_id) return;
    if (busy || active) return;
    setBusy(true); setError("");
    try {
      const turn = await api.turn(session.project_id, session.id, record.turn_id);
      setEditingMessage({ recordID: record.id, turn, text });
      setEditError("");
    } catch (reason) {
      setError(errorText(reason));
    } finally {
      setBusy(false);
    }
  }

  async function saveEditedMessage() {
    if (!editingMessage || !editingMessage.text.trim() || busy || active) return;
    setBusy(true); setEditError("");
    try {
      const result = await api.editMessage(session.project_id, session.id, editingMessage.turn, editingMessage.text.trim());
      // 阻止提交前的轮询覆盖新状态；先移除旧回复，再读取本轮生成进度。
      refreshVersion.current++;
      setSnapshot((current) => {
        if (!current) return current;
        const position = current.messages.findIndex((record) => record.id === editingMessage.recordID);
        return { ...current, turn: result.turn, events: [], messages: position < 0 ? [] : current.messages.slice(0, position + 1).map((record, index) => index === position ? { ...record, content_json: JSON.stringify({ role: "user", content_blocks: [{ type: "text", text: { text: editingMessage.text.trim() } }] }) } : record) };
      });
      setEditingMessage(null);
      await refresh();
    } catch (reason) {
      setEditError(errorText(reason));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="conversation">
      <header className="conversation-header"><div><Icon name="chat" size={18} /><h2>{session.title}</h2>{session.archived && <span className="tag neutral">已归档</span>}{snapshot?.turn && <span className="tag">{snapshot.turn.state}</span>}</div><div className="conversation-actions">{devToolsVisible && <button className="icon-button" aria-label="开发运行详情" title="开发运行详情" onClick={() => setRuntimeOpen(true)}><Icon name="target" size={17} /></button>}<button className="icon-button" aria-label="重命名会话" title="重命名会话" onClick={() => setRenaming(true)}><Icon name="edit" size={17} /></button><button className="icon-button" aria-label={session.archived ? "恢复会话" : "归档会话"} title={session.archived ? "恢复会话" : "归档会话"} disabled={busy} onClick={archive}><Icon name="archive" size={17} /></button><button className="icon-button danger-icon" aria-label="删除会话" title="删除会话" disabled={busy} onClick={() => setDeleting(true)}><Icon name="trash" size={17} /></button></div></header>
      <div className="conversation-body" role="region" aria-label="会话内容" tabIndex={0} ref={scrollArea} onScroll={(event) => {
        const area = event.currentTarget;
        followMessages.current = area.scrollHeight - area.clientHeight - area.scrollTop < 96;
      }}>
      {error && <p className="form-error" role="alert">{error}</p>}
      {messages.length === 0 ? <div className="conversation-empty"><div className="conversation-emblem"><Icon name="spark" size={30} /></div><span className="eyebrow">AUTO RESEARCH WORKSPACE</span><h2>从一个具体研究问题开始</h2><p>助手会先读取项目资料，必要时询问研究范围，<br />再把可执行的关注点整理进项目笔记。</p><div className="conversation-hints"><span><Icon name="paper" size={15} />整理方向</span><span><Icon name="branch" size={15} />追问范围</span><span><Icon name="target" size={15} />保存笔记</span></div></div> : <div className="conversation-messages" aria-live="polite">{progress.length > 0 && <section className="research-progress-list" aria-label="本轮研究进度">{progress.map((item) => <article className={`research-progress-card ${item.status}`} key={item.id}><div className="research-progress-heading"><span>{item.title}</span><small>{item.status === "waiting" ? "等待补充" : item.status === "updated" ? "已更新" : "已完成"}</small></div><p>{item.summary}</p>{item.facts && item.facts.length > 0 && <ul>{item.facts.map((fact) => <li key={fact}>{fact}</li>)}</ul>}{item.next_actions && item.next_actions.length > 0 && <div className="research-progress-actions">{item.next_actions.map((action) => <span key={action}>{action}</span>)}</div>}</article>)}</section>}{workspaceTools.length > 0 && <section className="workspace-tools-list" aria-label="工作区工具调用">{workspaceTools.filter((item) => item.tool !== "search_papers" || item.is_error).map((item) => <article className={`workspace-tool-card ${item.status}`} key={item.call_id}><div><strong>{item.tool}</strong><small>{item.status === "failed" ? "执行失败" : "已完成"}</small></div>{item.preview && <p>{item.preview}</p>}</article>)}</section>}{messages.map((message, index) => <article className={`conversation-message ${message.role}`} key={`${message.record.id}-${message.text.slice(0, 12)}`}><span>{message.role === "user" ? "你" : "研究助手"}</span>{message.report && <Link className="message-report-reference" to={`/p/${encodeURIComponent(session.project_id)}/research/${encodeURIComponent(message.report.run_id)}?report=${encodeURIComponent(message.report.report_id)}`}>引用报告 · v{message.report.revision} · {message.report.title}</Link>}{editingMessage?.recordID === message.record.id ? <MessageEditor value={editingMessage.text} error={editError} busy={busy} onChange={(text) => setEditingMessage((current) => current ? { ...current, text } : null)} onCancel={() => { setEditingMessage(null); setEditError(""); }} onSave={saveEditedMessage} /> : message.role === "assistant" ? <AssistantContent text={message.text} /> : <p>{message.text}</p>}<div className="message-actions"><button type="button" className="message-action" aria-label="复制消息" onClick={() => void copyMessage(message.text, index)}>{copiedMessage === index ? "已复制" : <Icon name="copy" size={14} />}</button>{message.role === "user" && <button type="button" className="message-action" aria-label="编辑消息" title="编辑消息" disabled={busy || active || !!editingMessage} onClick={() => void beginEdit(message.record, message.text)}><Icon name="edit" size={14} /></button>}</div></article>)}{streamingText && active && <article className="conversation-message assistant streaming"><span>研究助手</span><AssistantContent text={streamingText} /><i className="streaming-cursor" aria-label="正在生成" /></article>}{active && !question && <p className="conversation-progress">研究助手正在整理项目资料…</p>}</div>}
      <PaperSearchResults projectID={session.project_id} tools={workspaceTools} papers={papers} onAdd={onAddPaper} />
      {workspaceTools.some((item) => item.direction) && <section className="direction-proposal-cards" aria-label="研究方向提案">{workspaceTools.filter((item) => item.direction).map((item) => <article className="card" key={item.call_id}><strong>{item.direction!.name}</strong><p>{item.direction!.description}</p><p>已保存为待确认提案，编辑与当前状态请在方向页查看。</p><Link className="button secondary" to={`/p/${session.project_id}/directions?direction=${item.direction!.id}`}>查看并确认方向</Link></article>)}</section>}
      {snapshot?.turn && snapshot.turn.execution_kind !== "chat" && !snapshot.turn.definition_version?.endsWith("-v7") && <SessionResearchAnalysis projectID={session.project_id} turnID={snapshot.turn.id} onChanged={() => void refresh()} />}
      <ResearchTaskCards projectID={session.project_id} sessionID={session.id} />
      <ProjectNotes projectID={session.project_id} sessionID={session.id} turnID={snapshot?.turn.id} notes={notes} onDeleted={(noteID) => {
        setNotes((current) => current.filter((note) => note.id !== noteID));
        void refresh();
      }} />
      {turnFailureText(snapshot?.turn) && <div className="conversation-failure" role="alert"><strong>本轮执行失败</strong><p>{turnFailureText(snapshot?.turn)}</p><small>{turnFailureHint(snapshot?.turn)}</small></div>}
      </div>
      {snapshot && <UserInteraction projectID={session.project_id} target={{ kind: "session", id: session.id }} turn={snapshot.turn} question={question} onSubmitted={refresh} />}
      <div className="composer">
        {reportBinding.loading && <p role="status">正在载入报告引用…</p>}
        {reportBinding.error && <p className="action-error" role="alert">{reportBinding.error} <button className="text-button" onClick={reportBinding.retry}>重新载入报告</button> <button className="text-button" onClick={reportBinding.remove}>移除无效报告引用</button></p>}
        {reportBinding.context && <ReportContextCard projectID={session.project_id} context={reportBinding.context} disabled={busy || active || !!editingMessage || reportBinding.loading} onChoose={reportBinding.choose} onRemove={reportBinding.remove} />}
        {reportBinding.selection?.clear && <p className="muted">下一条消息将移除报告引用。</p>}
        <AgentModeSelector mode={agentMode.mode} onChange={agentMode.choose} disabled={agentMode.loading || busy} error={agentMode.error} onRetry={agentMode.reload} />
        <div className="composer-input"><textarea aria-label="消息" disabled={busy || active || !!editingMessage} value={input} onChange={(event) => setInput(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); void send(); } }} placeholder={active ? "本轮执行中，请等待结果" : reportBinding.context ? "围绕这份报告继续提问…" : "描述你想整理的研究问题…"} rows={2} /><button className="send-button" aria-label="发送消息" disabled={busy || active || !!editingMessage || agentMode.loading || !!agentMode.error || reportBinding.loading || !!reportBinding.error || !input.trim()} onClick={() => void send()}><Icon name="send" size={18} /></button></div><p>{active ? "当前会话有一轮研究任务正在执行" : "Enter 发送，Shift + Enter 换行"}</p>
      </div>
      {renaming && <RenameSessionDialog session={session} onClose={() => setRenaming(false)} onSaved={(updated) => { onSaved(updated); setRenaming(false); }} />}
      {deleting && <DeleteSessionDialog session={session} onClose={() => setDeleting(false)} onDeleted={onDeleted} />}
      {runtimeOpen && <RuntimeDetails events={snapshot?.events || []} turn={snapshot?.turn} onClose={() => setRuntimeOpen(false)} />}
    </section>
  );
}

