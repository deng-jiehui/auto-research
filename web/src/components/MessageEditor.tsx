export function MessageEditor({ value, error, busy, onChange, onCancel, onSave }: {
  value: string;
  error: string;
  busy: boolean;
  onChange: (value: string) => void;
  onCancel: () => void;
  onSave: () => Promise<void>;
}) {
  return <form className="message-editor" aria-label="编辑原消息" onSubmit={(event) => { event.preventDefault(); void onSave(); }}>
    <textarea autoFocus aria-label="修改消息" value={value} maxLength={8000} rows={4} disabled={busy} onChange={(event) => onChange(event.target.value)} />
    <p>保存后将替换此消息之后的对话，已保存的项目资料会保留。</p>
    {error && <p className="form-error" role="alert">{error}</p>}
    <div className="message-editor-actions">
      <button type="button" className="button secondary" disabled={busy} onClick={onCancel}>取消编辑</button>
      <button type="submit" className="button primary" disabled={busy || !value.trim()}>{busy ? "正在提交…" : "保存并重新发送"}</button>
    </div>
  </form>;
}
