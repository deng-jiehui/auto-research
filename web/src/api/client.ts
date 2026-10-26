import type {
  EvidenceMatrix, ReportReference, ReportSelection, ResearchReport, ResearchRun, ResearchSpec, ResearchPlanDecision, CandidateReviewDecision, ResearchTaskSummary,
  Project,
  ProjectUpdate,
  Session,
  SessionSnapshot,
  ProjectNote,
  Turn,
	UITarget,
  ModelSettings,
  ModelSettingsUpdate,
  Paper,
  PaperCandidate,
  SourceDocument,
  Evidence,
  AgentMode,
  DocumentBlock,
  DocumentFetch,
  Direction,
  DirectionDraft,
} from "../types";

export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public requestID = "",
  ) {
    super(message);
    this.name = "APIError";
  }
}

export async function apiRequest<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  let response: Response;
  try {
    response = await fetch(path, {
      ...init,
      headers: { "Content-Type": "application/json", ...init.headers },
    });
  } catch (error) {
    if (error instanceof Error && error.name === "AbortError") throw error;
    throw new APIError(
      0,
      "offline",
      "无法连接本地服务，请确认服务已启动后重试。",
    );
  }
  if (response.status === 204) return undefined as T;
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    throw new APIError(
      response.status,
      "invalid_response",
      "服务返回了无法读取的响应，请稍后重试。",
    );
  }
  if (!response.ok) {
    const problem = body as {
      code?: string;
      message?: string;
      request_id?: string;
    };
    throw new APIError(
      response.status,
      problem?.code || "unknown",
      problem?.message || "操作未能完成，请重试。",
      problem?.request_id,
    );
  }
  return body as T;
}

const json = (method: string, value: unknown) => ({
  method,
  body: JSON.stringify(value),
});
const projectURL = (id: string) => `/api/projects/${encodeURIComponent(id)}`;
const sessionURL = (projectID: string, sessionID: string) =>
  `${projectURL(projectID)}/sessions/${encodeURIComponent(sessionID)}`;
const interactionURL = (projectID: string, target: UITarget) => target.kind === "research"
  ? `${projectURL(projectID)}/research/${encodeURIComponent(target.id)}` : sessionURL(projectID, target.id);

export const api = {
  respondToInteraction: (projectID: string, target: UITarget, turn: Pick<Turn, "id" | "revision">, answers: { interrupt_id: string; text?: string; research_plan?: ResearchPlanDecision; research_candidates?: CandidateReviewDecision }[], operationID: string) =>
    apiRequest<{ turn: Turn; replayed: boolean }>(`${interactionURL(projectID, target)}/turns/${encodeURIComponent(turn.id)}/resume`, json("POST", { revision: turn.revision, idempotency_key: operationID, answers })),
  interactionEventsURL: (projectID: string, target: UITarget, after = 0) => `${interactionURL(projectID, target)}/events?after=${after}&limit=100`,
  researchMatrix: (projectID: string, runID: string) => apiRequest<EvidenceMatrix>(`${projectURL(projectID)}/research/${encodeURIComponent(runID)}/matrix`),
  researchReports: (projectID: string, runID: string, signal?: AbortSignal) => apiRequest<{ items: ReportReference[] }>(`${projectURL(projectID)}/research/${encodeURIComponent(runID)}/reports`, { signal }),
  researchReport: (projectID: string, reportID: string, signal?: AbortSignal) => apiRequest<ResearchReport>(`${projectURL(projectID)}/reports/${encodeURIComponent(reportID)}`, { signal }),

  directions: (projectID: string, signal?: AbortSignal) =>
    apiRequest<{ items: Direction[] }>(`${projectURL(projectID)}/directions`, { signal }),
  direction: (projectID: string, id: string, signal?: AbortSignal) =>
    apiRequest<Direction>(`${projectURL(projectID)}/directions/${encodeURIComponent(id)}`, { signal }),
  proposeDirection: (projectID: string, draft: DirectionDraft) =>
    apiRequest<Direction>(`${projectURL(projectID)}/directions`, json("POST", { ...draft, idempotency_key: crypto.randomUUID() })),
  editDirection: (projectID: string, id: string, revision: number, draft: DirectionDraft) =>
    apiRequest<Direction>(`${projectURL(projectID)}/directions/${encodeURIComponent(id)}`, json("PATCH", { ...draft, revision })),
  decideDirection: (projectID: string, id: string, revision: number, action: "accept" | "reject" | "archive") =>
    apiRequest<Direction>(`${projectURL(projectID)}/directions/${encodeURIComponent(id)}/${action}`, json("POST", { revision })),
  directionHistory: (projectID: string, id: string, signal?: AbortSignal) =>
    apiRequest<{ items: Direction[] }>(`${projectURL(projectID)}/directions/${encodeURIComponent(id)}/history`, { signal }),
  fetchPaperDocument: (projectID: string, paperID: string, refresh = false) =>
    apiRequest<SourceDocument>(`${projectURL(projectID)}/papers/${encodeURIComponent(paperID)}/documents/fetch`, json("POST", { refresh })),
  documentFetchStatus: (projectID: string, paperID: string, signal?: AbortSignal) =>
    apiRequest<DocumentFetch>(`${projectURL(projectID)}/papers/${encodeURIComponent(paperID)}/documents/status`, { signal }),
  documentContents: (projectID: string, documentID: string, signal?: AbortSignal) =>
    apiRequest<SourceDocument>(`${projectURL(projectID)}/documents/${encodeURIComponent(documentID)}`, { signal }),
  documentBlock: (projectID: string, documentID: string, blockID: string, signal?: AbortSignal) =>
    apiRequest<DocumentBlock>(`${projectURL(projectID)}/documents/${encodeURIComponent(documentID)}/blocks/${encodeURIComponent(blockID)}`, { signal }),
  projects: (archived = false, signal?: AbortSignal) =>
    apiRequest<{ items: Project[] }>(`/api/projects?archived=${archived}`, {
      signal,
    }),
  project: (id: string, signal?: AbortSignal) =>
    apiRequest<Project>(projectURL(id), { signal }),
  createProject: (title: string) =>
    apiRequest<Project>("/api/projects", json("POST", { title })),
  updateProject: (id: string, data: ProjectUpdate) =>
    apiRequest<Project>(projectURL(id), json("PATCH", data)),
  archiveProject: (project: Project, archived: boolean) =>
    apiRequest<Project>(
      `${projectURL(project.id)}/archive`,
      json("POST", { archived, revision: project.revision }),
    ),
  deleteProject: (project: Project, confirm_title: string) =>
    apiRequest<void>(
      projectURL(project.id),
      json("DELETE", { confirm_title, revision: project.revision }),
    ),
  sessions: (id: string, archived = false, signal?: AbortSignal) =>
    apiRequest<{ items: Session[] }>(
      `${projectURL(id)}/sessions?archived=${archived}`,
      { signal },
    ),
  session: (id: string, sessionID: string, signal?: AbortSignal) =>
    apiRequest<Session>(
      `${projectURL(id)}/sessions/${encodeURIComponent(sessionID)}`,
      { signal },
    ),
  createSession: (id: string) =>
    apiRequest<Session>(
      `${projectURL(id)}/sessions`,
      json("POST", { title: "新会话" }),
    ),
  updateSession: (
    id: string,
    sessionID: string,
    data: Partial<Pick<Session, "title" | "archived">>,
  ) =>
    apiRequest<Session>(
      `${projectURL(id)}/sessions/${encodeURIComponent(sessionID)}`,
      json("PATCH", data),
    ),
  deleteSession: (id: string, sessionID: string) =>
    apiRequest<void>(
      `${projectURL(id)}/sessions/${encodeURIComponent(sessionID)}`,
      { method: "DELETE" },
    ),
  snapshot: (projectID: string, sessionID: string, signal?: AbortSignal) =>
    apiRequest<SessionSnapshot>(`${sessionURL(projectID, sessionID)}/snapshot`, {
      signal,
    }),
  notes: (projectID: string) =>
    apiRequest<{ items: ProjectNote[] }>(`${projectURL(projectID)}/notes`),
  deleteNote: (projectID: string, noteID: string) =>
    apiRequest<void>(`${projectURL(projectID)}/notes/${encodeURIComponent(noteID)}`, { method: "DELETE" }),
  papers: (projectID: string, query = "", signal?: AbortSignal) =>
    apiRequest<{ items: Paper[] }>(`${projectURL(projectID)}/papers${query ? `?${query}` : ""}`, { signal }),
  paper: (projectID: string, paperID: string, signal?: AbortSignal) =>
    apiRequest<Paper>(`${projectURL(projectID)}/papers/${encodeURIComponent(paperID)}`, { signal }),
  importPaper: (projectID: string, candidate: PaperCandidate) =>
    apiRequest<Paper>(`${projectURL(projectID)}/papers`, json("POST", candidate)),
  updatePaper: (projectID: string, paperID: string, data: { note?: string; tags?: string[]; reading_state?: string; revision: number }) =>
    apiRequest<Paper>(`${projectURL(projectID)}/papers/${encodeURIComponent(paperID)}`, json("PATCH", data)),
  academicStatus: (projectID: string, signal?: AbortSignal) =>
    apiRequest<{ mode: string; configured: boolean; read_scope: string }>(`/api/projects/${projectID}/academic/status`, { signal }),
  searchAcademic: (projectID: string, query: { text: string; year_from?: number; year_to?: number; limit?: number }) =>
    apiRequest<{ items: PaperCandidate[] }>(`${projectURL(projectID)}/academic/search`, json("POST", query)),
  paperDocuments: (projectID: string, paperID: string) =>
    apiRequest<{ items: SourceDocument[] }>(`${projectURL(projectID)}/papers/${encodeURIComponent(paperID)}/documents`),
  savePaperDocument: (projectID: string, paperID: string, document: { text: string; locator?: string }) =>
    apiRequest<SourceDocument>(`${projectURL(projectID)}/papers/${encodeURIComponent(paperID)}/documents`, json("POST", document)),
  paperEvidence: (projectID: string, paperID: string) =>
    apiRequest<{ items: Evidence[] }>(`${projectURL(projectID)}/papers/${encodeURIComponent(paperID)}/evidence`),
  savePaperEvidence: (projectID: string, paperID: string, evidence: Pick<Evidence, "document_id" | "quote" | "field" | "value"> & Partial<Pick<Evidence, "block_id" | "start_rune" | "end_rune">>) =>
    apiRequest<Evidence>(`${projectURL(projectID)}/papers/${encodeURIComponent(paperID)}/evidence`, json("POST", evidence)),
  eventsURL: (projectID: string, sessionID: string, after = 0) =>
    `${sessionURL(projectID, sessionID)}/events?after=${after}&limit=100`,
  turn: (projectID: string, sessionID: string, turnID: string) =>
    apiRequest<Turn>(
      `${sessionURL(projectID, sessionID)}/turns/${encodeURIComponent(turnID)}`,
    ),
  controlResearch: (projectID: string, runID: string, kind: "pause" | "continue" | "retry" | "cancel", input: {revision: number; idempotency_key: string; paper_id?: string; work_revision?: number}) => apiRequest<{ turn: Turn; replayed: boolean }>(`/api/projects/${encodeURIComponent(projectID)}/research/${encodeURIComponent(runID)}/${kind}`, json("POST",input)),
  researchRuns: (projectID: string, signal?: AbortSignal) => apiRequest<{ items: ResearchRun[] }>(`${projectURL(projectID)}/research`, { signal }),
  createResearchTask: (projectID: string, spec: ResearchSpec, operationID: string, origin?: { origin_session_id: string; origin_turn_id?: string }) => apiRequest<{ run: ResearchRun; turn: Turn; replayed: boolean }>(`${projectURL(projectID)}/research`, json("POST", { ...spec, ...origin, idempotency_key: operationID })),
  researchSnapshot: (projectID: string, runID: string, signal?: AbortSignal) => apiRequest<SessionSnapshot>(`${projectURL(projectID)}/research/${encodeURIComponent(runID)}/snapshot`, { signal }),
  sessionResearch: (projectID: string, sessionID: string, signal?: AbortSignal) => apiRequest<{ items: ResearchTaskSummary[] }>(`${sessionURL(projectID, sessionID)}/research`, { signal }),
  startResearch: (projectID: string, sessionID: string, research: ResearchSpec, operationID?: string) => apiRequest<{ turn: Turn }>(`${sessionURL(projectID,sessionID)}/messages`, json("POST", {text: research.question, idempotency_key: operationID || crypto.randomUUID(), agent_mode: "single", research})),
  researchRun: (projectID: string, runID: string, signal?: AbortSignal) => apiRequest<ResearchRun>(`/api/projects/${encodeURIComponent(projectID)}/research/${encodeURIComponent(runID)}`, { signal }),
  deleteResearch: (projectID: string, runID: string) => apiRequest<void>(`/api/projects/${encodeURIComponent(projectID)}/research/${encodeURIComponent(runID)}`, { method: "DELETE" }),
  reviewResearchCandidates: (projectID: string, sessionID: string, turn: Turn, interruptID: string, review: CandidateReviewDecision, operationID: string) => apiRequest(`${sessionURL(projectID,sessionID)}/turns/${turn.id}/resume`, json("POST", {revision: turn.revision, idempotency_key: operationID, answers: [{interrupt_id:interruptID, research_candidates:review}]})),
  reviewResearchPlan: (projectID: string, sessionID: string, turn: Turn, interruptID: string, review: ResearchPlanDecision, operationID: string) => apiRequest(`${sessionURL(projectID,sessionID)}/turns/${turn.id}/resume`, json("POST", {revision: turn.revision, idempotency_key: operationID, answers: [{interrupt_id:interruptID, research_plan:review}]})),
  agentSettings: () => apiRequest<{ mode: AgentMode }>("/api/runtime/agent"),
  sendMessage: (projectID: string, sessionID: string, text: string, agentMode?: AgentMode, reportContext?: ReportSelection) =>
    apiRequest<{ turn: Turn }>(`${sessionURL(projectID, sessionID)}/messages`,
      json("POST", { text, idempotency_key: crypto.randomUUID(), agent_mode: agentMode, report_context: reportContext })),
  resumeTurn: (
    projectID: string,
    sessionID: string,
    turn: Turn,
    interruptID: string,
    text: string,
  ) =>
    apiRequest(`${sessionURL(projectID, sessionID)}/turns/${turn.id}/resume`,
      json("POST", {
        revision: turn.revision,
        idempotency_key: crypto.randomUUID(),
        answers: [{ interrupt_id: interruptID, text }],
      })),
  modelSettings: () => apiRequest<ModelSettings>("/api/runtime/model"),
  updateModelSettings: (data: ModelSettingsUpdate) =>
    apiRequest<ModelSettings>("/api/runtime/model", {
      method: "PUT",
      body: JSON.stringify(data),
    }),
  retryResearchTask: (projectID: string, sessionID: string, turn: Pick<Turn, "id" | "revision">, operationID: string) => apiRequest<{ turn: Turn; replayed: boolean }>(`${sessionURL(projectID, sessionID)}/turns/${encodeURIComponent(turn.id)}/retry`, json("POST", { revision: turn.revision, idempotency_key: operationID })),
  retryTurn: (projectID: string, sessionID: string, turn: Pick<Turn, "id" | "revision">) =>
    apiRequest<{ turn: Turn; replayed: boolean }>(
      `${sessionURL(projectID, sessionID)}/turns/${encodeURIComponent(turn.id)}/retry`,
      json("POST", { revision: turn.revision, idempotency_key: crypto.randomUUID() }),
    ),
  editMessage: (projectID: string, sessionID: string, turn: Pick<Turn, "id" | "revision">, text: string) =>
    apiRequest<{ turn: Turn; replayed: boolean }>(
      `${sessionURL(projectID, sessionID)}/turns/${encodeURIComponent(turn.id)}/edit`,
      json("POST", { text, revision: turn.revision, idempotency_key: crypto.randomUUID() }),
    ),
};

export const errorText = (error: unknown) =>
  error instanceof Error ? error.message : "操作未能完成，请重试。";
