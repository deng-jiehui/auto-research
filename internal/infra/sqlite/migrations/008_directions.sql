CREATE TABLE directions (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL,
    state TEXT NOT NULL,
    content_json TEXT NOT NULL
);
CREATE INDEX directions_scope ON directions(project_id,owner_id);
CREATE TABLE direction_revisions (
    direction_id TEXT NOT NULL REFERENCES directions(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL,
    content_json TEXT NOT NULL,
    PRIMARY KEY(direction_id,revision)
);
CREATE TABLE direction_submissions (
    owner_id TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    submission_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    direction_id TEXT NOT NULL REFERENCES directions(id) ON DELETE CASCADE,
    content_json TEXT NOT NULL,
    PRIMARY KEY(owner_id,project_id,submission_key)
);
