import { Link } from "react-router-dom";
import type { Evidence } from "../types";

export function CitationLink({ projectID, evidence }: { projectID: string; evidence: Evidence }) {
  const query = new URLSearchParams({ paper: evidence.paper_id, evidence: evidence.id });
  return <Link className="text-button" to={`/p/${encodeURIComponent(projectID)}/papers?${query}`}>查看原文</Link>;
}
