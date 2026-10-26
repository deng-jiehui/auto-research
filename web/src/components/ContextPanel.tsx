import { Link } from "react-router-dom";
import type { Paper, Project } from "../types";
import { Icon } from "./Icon";

export function ContextPanel({
  project,
  onEdit,
  papers,
  papersLoading,
  papersError,
  onRetryPapers,
}: {
  project: Project;
  onEdit: () => void;
  papers: Paper[];
  papersLoading: boolean;
  papersError: string;
  onRetryPapers: () => void;
}) {
  const settings = project.settings;
  return (
    <aside className="context-panel" aria-label="研究上下文">
      <div className="context-heading">
        <span>
          <Icon name="target" size={18} />
          研究上下文
        </span>
        <button
          className="icon-button"
          aria-label="编辑研究上下文"
          onClick={onEdit}
        >
          <Icon name="edit" size={16} />
        </button>
      </div>
      <section className="context-section">
        <h2>研究问题</h2>
        <p className={project.question ? "question-text" : "muted"}>
          {project.question ||
            "还没有确定研究问题。先记录一个想法，之后可以逐步完善。"}
        </p>
        {project.field && (
          <span className="tag field-tag">{project.field}</span>
        )}
      </section>
      <section className="context-section">
        <h2>研究范围</h2>
        <dl className="scope-list">
          <div>
            <dt>论文来源</dt>
            <dd>
              <Icon name="globe" size={14} />
              arXiv
            </dd>
          </div>
          <div>
            <dt>发表年份</dt>
            <dd>
              {settings.year_from
                ? `${settings.year_from}–${settings.year_to}`
                : "不限年份"}
            </dd>
          </div>
          <div>
            <dt>候选论文</dt>
            <dd>最多 {settings.candidate_limit} 篇</dd>
          </div>
          <div>
            <dt>深入分析</dt>
            <dd>最多 {settings.analyze_limit} 篇</dd>
          </div>
        </dl>
      </section>
      <section className="context-section">
        <h2>
          参考资料<span className="soft-label">{papers.length} 篇</span>
        </h2>
        {papersLoading ? <p className="muted">正在读取论文库…</p> : papersError ? <p className="form-error" role="alert">{papersError} <button type="button" className="text-button" onClick={onRetryPapers}>重新加载</button></p> : papers.length > 0 ? <>
          <p className="reference-help">项目共享资料，助手可按需读取。</p>
          <ul className="reference-papers">{papers.slice(0, 8).map((paper) => <li key={paper.id}>
            <Link to={`/p/${project.id}/papers?paper=${encodeURIComponent(paper.id)}`} aria-label={`打开论文：${paper.title}`}><Icon name="paper" size={15} /><span>{paper.title}<small>{paper.source_key}</small></span></Link>
          </li>)}</ul>
        </> : <div className="context-placeholder">
          <Icon name="paper" size={22} />
          <p>还没有加入参考资料</p>
          <span>让助手检索论文，点击结果上的“加入论文库”，也可进入论文库手动添加。</span>
        </div>}
        <Link className="button secondary reference-library-link" to={`/p/${project.id}/papers`}>打开论文库</Link>
      </section>
      <div className="context-note">
        <Icon name="spark" size={16} />
        <p>
          好的研究，从一个清晰的问题开始。
          <br />
          项目设置可以随时调整。
        </p>
      </div>
    </aside>
  );
}
