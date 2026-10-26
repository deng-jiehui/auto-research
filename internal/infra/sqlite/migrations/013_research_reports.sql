CREATE TABLE research_reports (
 id TEXT PRIMARY KEY,
 owner_id TEXT NOT NULL,
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 run_id TEXT NOT NULL REFERENCES research_runs(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL,
 content_json TEXT NOT NULL,
 UNIQUE(run_id,revision)
);
CREATE TABLE research_report_operations (
 owner_id TEXT NOT NULL,
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 operation_id TEXT NOT NULL,
 turn_id TEXT NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
 request_hash TEXT NOT NULL,
 report_id TEXT NOT NULL REFERENCES research_reports(id) ON DELETE CASCADE,
 PRIMARY KEY(owner_id,project_id,operation_id)
);
