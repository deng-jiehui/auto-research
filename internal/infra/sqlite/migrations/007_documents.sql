ALTER TABLE source_documents ADD COLUMN content_kind TEXT NOT NULL DEFAULT 'abstract';
ALTER TABLE source_documents ADD COLUMN source_version TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE source_documents ADD COLUMN parser_version TEXT NOT NULL DEFAULT '';
ALTER TABLE source_documents ADD COLUMN original_artifact_id TEXT NOT NULL DEFAULT '';
ALTER TABLE evidence ADD COLUMN block_id TEXT NOT NULL DEFAULT '';

CREATE TABLE document_blocks (
    id TEXT NOT NULL,
    document_id TEXT NOT NULL REFERENCES source_documents(id) ON DELETE CASCADE,
    owner_id TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    paper_id TEXT NOT NULL REFERENCES papers(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    text TEXT NOT NULL,
    start_rune INTEGER NOT NULL,
    end_rune INTEGER NOT NULL,
    UNIQUE(document_id,id)
);
CREATE INDEX document_blocks_project ON document_blocks(project_id,owner_id);
CREATE VIRTUAL TABLE document_blocks_fts USING fts5(text,content='document_blocks',content_rowid='rowid');
CREATE TRIGGER document_blocks_ai AFTER INSERT ON document_blocks BEGIN
    INSERT INTO document_blocks_fts(rowid,text) VALUES(new.rowid,new.text);
END;
CREATE TRIGGER document_blocks_ad AFTER DELETE ON document_blocks BEGIN
    INSERT INTO document_blocks_fts(document_blocks_fts,rowid,text) VALUES('delete',old.rowid,old.text);
END;

CREATE TABLE document_fetches (
    paper_id TEXT PRIMARY KEY REFERENCES papers(id) ON DELETE CASCADE,
    owner_id TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    state TEXT NOT NULL,
    document_id TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
