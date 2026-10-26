CREATE TABLE research_candidate_versions (
    run_id TEXT NOT NULL REFERENCES research_runs(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL,
    snapshot_json TEXT NOT NULL,
    PRIMARY KEY(run_id,revision)
);
CREATE TABLE research_candidate_reviews (
    run_id TEXT NOT NULL REFERENCES research_runs(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL,
    review_json TEXT NOT NULL,
    PRIMARY KEY(run_id,revision)
);
