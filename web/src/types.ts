export interface ResearchSettings {
  year_from: number;
  year_to: number;
  providers: string[];
  candidate_limit: number;
  analyze_limit: number;
}

export interface Project {
  id: string;
  owner_id: string;
  title: string;
  question: string;
  field: string;
  settings: ResearchSettings;
  archived: boolean;
  revision: number;
}

export interface Session {
  kind?: "chat" | "research";
  id: string;
  project_id: string;
  title: string;
  archived: boolean;
}

export interface Turn {
  report_context?: ReportContext;
  execution_kind?: "chat" | "research";
  definition_version?: string;
  id: string;
  session_id: string;
  project_id: string;
  state: string;
  input_text: string;
  revision: number;
  pending_questions_json?: string;
  error_code?: string;
  error_message?: string;
}

export interface MessageRecord {
  id: string;
  turn_id?: string;
  attempt?: number;
  content_json: string;
  committed: boolean;
}

export interface SessionEvent {
  id: number;
  session_id?: string;
  turn_id?: string;
  attempt?: number;
  kind: string;
  payload_json: string;
  stream_run_id?: string;
  sequence?: number;
  source_checkpoint_id?: string;
}

export interface ResearchProgress {
  id: string;
  stage: string;
  status: string;
  title: string;
  summary: string;
  facts?: string[];
  next_actions?: string[];
}

export interface WorkspaceToolEvent {
  tool: string;
  call_id: string;
  status: "completed" | "failed";
  is_error: boolean;
  preview?: string;
  papers?: PaperCandidate[];
  direction?: Direction;
}

export interface DirectionDraft {
  name: string;
  description: string;
  paper_ids?: string[];
  questions?: string[];
  gap?: string;
  feasibility?: string;
}

export interface Direction extends DirectionDraft {
  id: string;
  project_id: string;
  state: "proposed" | "accepted" | "rejected" | "archived";
  revision: number;
  source_session_id?: string;
  source_turn_id?: string;
  evidence_insufficient: boolean;
}

export type AgentMode = "single" | "multi";

export interface ProjectNote {
  id: string;
  text: string;
  created_at: string;
  source_session_id?: string;
  source_turn_id?: string;
}

export interface Paper {
  id: string;
  project_id: string;
  source_key: string;
  title: string;
  authors: string[];
  year: number;
  doi?: string;
  arxiv_id?: string;
  url?: string;
  abstract?: string;
  tags: string[];
  note: string;
  reading_state: string;
  revision: number;
}

export interface PaperCandidate {
  source_key?: string;
  title: string;
  url?: string;
  doi?: string;
  arxiv_id?: string;
  authors?: string[];
  year?: number;
  abstract?: string;
  provider?: string;
}

export interface SourceDocument {
  id: string;
  paper_id: string;
  artifact_id: string;
  text: string;
  sha256: string;
  locator: string;
  content_kind?: string;
  source_version?: string;
  parser_version?: string;
  blocks?: DocumentBlock[];
}

export interface DocumentBlock {
  id: string;
  document_id: string;
  paper_id: string;
  title: string;
  text?: string;
  start_rune: number;
  end_rune: number;
}

export interface DocumentFetch {
  paper_id: string;
  state: "idle" | "running" | "ready" | "failed";
  document_id?: string;
  error?: string;
}

export interface Evidence {
  id: string;
  project_id: string;
  paper_id: string;
  document_id: string;
  block_id?: string;
  field: string;
  value: string;
  quote: string;
  locator: string;
  start_rune: number;
  end_rune: number;
}

export interface SessionSnapshot {
	ui?: UIState;
	research?: ResearchRun;
  turn: Turn;
  messages: MessageRecord[];
  notes: ProjectNote[];
  events: SessionEvent[];
}

export interface UITarget { kind: "session" | "research"; id: string; }
export interface UIInteraction {
  id: string; kind: string; question: string; choices?: string[]; allow_custom?: boolean; placeholder?: string;
  run_id?: string; revision?: number; hash?: string; plan?: ResearchPlan; candidate_ids?: string[]; analyze_limit?: number;
}
export interface UIState {
  version: string; target: UITarget; event_cursor: number; interaction: UIInteraction | null;
  interaction_error?: string; available_actions: string[];
}

export interface ModelSettings {
  mode: "fixture" | "remote";
  endpoint?: string;
  model?: string;
  configured: boolean;
}

export interface ModelSettingsUpdate {
  mode: "fixture" | "remote";
  endpoint: string;
  model: string;
  api_key: string;
}

export type ProjectUpdate = Pick<
  Project,
  "title" | "question" | "field" | "settings" | "revision"
>;

export interface ResearchPlan { summary: string; queries: string[]; inclusion?: string[]; exclusion?: string[]; }
export interface ResearchPlanPrompt { kind: "research_plan"; question: string; run_id: string; revision: number; hash: string; plan: ResearchPlan; }
export interface ResearchSpec { question: string; constraints?: string[]; paper_ids?: string[]; direction_id?: string; direction_revision?: number; settings?: ResearchSettings; model_call_limit?: number; tool_call_limit?: number; }
export interface ResearchTaskSummary { report_path?: string; id: string; question: string; state: string; phase: string; summary: string; task_url: string; resource_path: string; }
export interface ResearchPlanDecision { run_id: string; plan_revision: number; plan_hash: string; decision: "approve" | "cancel"; plan: ResearchPlan; }
export interface ResearchCandidate extends PaperCandidate { id: string; source_key: string; recommended: boolean; recommendation_reason?: string; }
export interface CandidateChoice { candidate_id: string; selected: boolean; reason: string; paper_id?: string; }
export interface CandidateReviewDecision { run_id: string; candidate_revision: number; candidate_hash: string; decision: "approve" | "search_again" | "cancel"; analyze_limit: number; choices: CandidateChoice[]; feedback?: string; }
export interface CandidateReviewPrompt { kind: "research_candidates"; question: string; run_id: string; revision: number; hash: string; candidate_ids?: string[]; analyze_limit: number; }
export interface ResearchRun { latest_report?: ReportReference; detached?: boolean; origin_session_id?: string; origin_turn_id?: string; previous_run_id?: string; work_items?: ResearchWorkItem[]; pause_requested?: boolean; candidates?: ResearchCandidate[]; candidate_round: number; candidate_searches: number; candidate_revision: number; candidate_hash: string; approved_candidate_revision: number; candidate_review?: CandidateReviewDecision; execution_state?: string; execution_attempt: number; error_message?: string; id: string; project_id: string; session_id: string; turn_id: string; phase: string; spec: ResearchSpec & { settings: ResearchSettings; model_call_limit: number; tool_call_limit: number }; spec_hash: string; project_snapshot: Project; plan: ResearchPlan; plan_revision: number; approved_plan_revision: number; model_calls: number; tool_calls: number; input_tokens: number; output_tokens: number; unknown_usage: number; }

export interface AnalysisField { name: string; status: string; text: string; evidence_ids?: string[]; }
export interface PaperAnalysis { paper_id: string; document_id?: string; status: string; summary?: string; error?: string; fields?: AnalysisField[]; }
export interface ResearchWorkItem { id: string; run_id: string; paper_id: string; title: string; document_id?: string; content_kind?: string; state: string; attempts: number; revision: number; analysis_revision: number; analysis_hash?: string; analysis?: PaperAnalysis; }

export interface MatrixRow { work_item_id: string; paper_id: string; title: string; document_id?: string; content_kind?: string; state: string; analysis_revision: number; analysis_hash?: string; analysis?: PaperAnalysis; }
export interface EvidenceMatrix { run_id: string; spec_hash: string; plan_hash: string; candidate_revision: number; hash: string; rows: MatrixRow[]; }
export interface ReportClaim { section: string; status: string; text: string; analysis_refs: { work_item_id: string; analysis_revision: number }[]; evidence_ids?: string[]; }
export interface ReportReference { id: string; revision: number; matrix_hash: string; summary: string; resource_path: string; }
export type ReportSelection = { run_id: string; report_id: string; clear?: false } | { clear: true };
export interface ReportContext {
  run_id: string; report_id: string; revision: number; title: string; summary: string; matrix_hash: string;
  resource_path: string; matrix_path: string; references_path: string; report_url: string;
}
export interface ResearchReport extends ReportReference { run_id: string; project_id: string; title: string; created_at: string; matrix: EvidenceMatrix; references: Evidence[]; markdown: string; draft: { title: string; summary: string; matrix_hash: string; limitations: string[]; claims: ReportClaim[] }; }
