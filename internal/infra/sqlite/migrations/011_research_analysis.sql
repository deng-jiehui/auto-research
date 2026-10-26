CREATE TABLE research_work_items (
 id TEXT PRIMARY KEY,
 run_id TEXT NOT NULL REFERENCES research_runs(id) ON DELETE CASCADE,
 paper_id TEXT NOT NULL,
 content_json TEXT NOT NULL,
 UNIQUE(run_id,paper_id)
);
CREATE TABLE research_analysis_versions (
 work_item_id TEXT NOT NULL REFERENCES research_work_items(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL,
 content_hash TEXT NOT NULL,
 content_json TEXT NOT NULL,
 PRIMARY KEY(work_item_id,revision)
);
