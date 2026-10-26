CREATE TABLE research_runs (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    turn_id TEXT NOT NULL UNIQUE REFERENCES turns(id) ON DELETE CASCADE,
    start_hash TEXT NOT NULL,
    content_json TEXT NOT NULL
);
CREATE INDEX research_scope ON research_runs(project_id,owner_id);
CREATE TABLE research_operations (
    owner_id TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    operation_id TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    result_json TEXT NOT NULL,
    PRIMARY KEY(owner_id,project_id,operation_id)
);
CREATE TABLE research_plan_versions (
    run_id TEXT NOT NULL REFERENCES research_runs(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL,
    plan_json TEXT NOT NULL,
    PRIMARY KEY(run_id,revision)
);
CREATE TABLE research_attempts (
    id TEXT PRIMARY KEY,
    turn_id TEXT NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    input_tokens INTEGER,
    output_tokens INTEGER,
    usage_recorded INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
