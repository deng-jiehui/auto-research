ALTER TABLE sessions ADD COLUMN kind TEXT NOT NULL DEFAULT 'chat';
ALTER TABLE turns ADD COLUMN execution_kind TEXT NOT NULL DEFAULT 'chat';
CREATE INDEX turns_execution_queue ON turns(execution_kind,state);
CREATE TABLE research_tasks (
 run_id TEXT PRIMARY KEY REFERENCES research_runs(id) ON DELETE CASCADE,
 owner_id TEXT NOT NULL,
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 origin_session_id TEXT NOT NULL DEFAULT '',
 origin_turn_id TEXT NOT NULL DEFAULT '',
 operation_id TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 UNIQUE(owner_id,project_id,operation_id)
);
CREATE INDEX research_task_origin ON research_tasks(project_id,owner_id,origin_session_id);
