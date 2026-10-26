import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { api, errorText } from "../api/client";
import type { DocumentBlock, Evidence, SourceDocument } from "../types";

export function DocumentReader({ projectID, documentID, evidence, onSaved }: {
  projectID: string;
  documentID: string;
  evidence?: Evidence;
  onSaved: (item: Evidence) => void;
}) {
  const [document, setDocument] = useState<SourceDocument | null>(null);
  const [blockID, setBlockID] = useState("");
  const [block, setBlock] = useState<DocumentBlock | null>(null);
  const [quoting, setQuoting] = useState(false);
  const [quote, setQuote] = useState("");
  const [field, setField] = useState("");
  const [value, setValue] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    setDocument(null);
    setBlock(null);
    setError("");
    api.documentContents(projectID, documentID, controller.signal).then((doc) => {
      setDocument(doc);
      const cited = evidence && doc.blocks?.find((item) => item.id === evidence.block_id ||
        (!evidence.block_id && item.start_rune <= evidence.start_rune && item.end_rune >= evidence.end_rune));
      setBlockID(cited?.id || doc.blocks?.[0]?.id || "");
    }).catch((reason) => { if (!controller.signal.aborted) setError(errorText(reason)); });
    return () => controller.abort();
  }, [projectID, documentID, evidence?.id]);

  useEffect(() => {
    if (!document || !blockID) return;
    const controller = new AbortController();
    setBlock(null);
    setQuoting(false);
    setMessage("");
    setError("");
    api.documentBlock(projectID, document.id, blockID, controller.signal).then(setBlock)
      .catch((reason) => { if (!controller.signal.aborted) setError(errorText(reason)); });
    return () => controller.abort();
  }, [projectID, document, blockID]);

  async function save(event: FormEvent) {
    event.preventDefault();
    if (!block || saving) return;
    setSaving(true);
    setError("");
    try {
      const item = await api.savePaperEvidence(projectID, block.paper_id, {
        document_id: documentID, block_id: block.id, quote, field: field.trim(), value: value.trim(),
      });
      onSaved(item);
      setMessage("证据已保存，可点击引用查看原文。");
      setQuoting(false);
    } catch (reason) {
      setError(errorText(reason));
    } finally {
      setSaving(false);
    }
  }

  function renderText() {
    const text = block?.text || "";
    if (!evidence || !block || evidence.document_id !== documentID || evidence.start_rune < block.start_rune || evidence.end_rune > block.end_rune) return text;
    // 后端区间以 Unicode 码点计数，不能用 UTF-16 下标切中文或表情符号。
    const runes = Array.from(text);
    const start = evidence.start_rune - block.start_rune;
    const end = evidence.end_rune - block.start_rune;
    return <>{runes.slice(0, start).join("")}<mark>{runes.slice(start, end).join("")}</mark>{runes.slice(end).join("")}</>;
  }

  return <section className="document-reader" aria-label="论文原文阅读器">
    <h3>{document?.content_kind === "abstract" || !document?.content_kind ? "来源文本" : "全文"}阅读器</h3>
    {document && <p className="muted">{document.locator} · 来源版本 {document.source_version || "unknown"} · 冻结文本 {document.sha256.slice(0, 10)}</p>}
    {error && <p className="action-error" role="alert">{error}</p>}
    {!document && !error && <p role="status">正在读取目录…</p>}
    {document && <div className="document-reader-layout">
      <nav aria-label="论文章节">{document.blocks?.map((item) => <button key={item.id} aria-current={item.id === blockID ? "true" : undefined} onClick={() => setBlockID(item.id)}>{item.title} · {item.id}</button>)}</nav>
      <div>
        {block ? <><pre className="document-block-text">{renderText()}</pre><button className="button secondary" onClick={() => { setQuote(block.text || ""); setQuoting(true); setMessage(""); }}>引用本段</button></> : <p role="status">正在读取片段…</p>}
        {quoting && <form className="evidence-form" onSubmit={save}>
          <label>原文引用<textarea aria-label="原文引用" value={quote} onChange={(event) => setQuote(event.target.value)} /></label>
          <p className="muted">可缩短为本段内唯一的逐字原文；保存时自动校验定位。</p>
          <label>证据字段<input aria-label="证据字段" placeholder="例如 method、dataset、limitation" value={field} onChange={(event) => setField(event.target.value)} /></label>
          <label>研究解读<input aria-label="研究解读" value={value} onChange={(event) => setValue(event.target.value)} /></label>
          <div className="paper-result-actions"><button className="button primary" disabled={saving || !quote.trim() || !field.trim()}>{saving ? "正在保存…" : "保存证据"}</button><button type="button" className="text-button" onClick={() => setQuoting(false)}>取消</button></div>
        </form>}
        {message && <p role="status">{message}</p>}
      </div>
    </div>}
  </section>;
}
