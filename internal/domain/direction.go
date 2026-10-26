package domain

import "context"

// DirectionDraft 只包含提案内容，模型不能通过参数写入批准状态。
type DirectionDraft struct {
	Name        string   `json:"name" jsonschema:"研究方向名称，最多 120 字符"`
	Description string   `json:"description" jsonschema:"方向说明，最多 4000 字符"`
	PaperIDs    []string `json:"paper_ids,omitempty" jsonschema:"本项目已归档的代表论文 ID；没有依据时留空，不编造"`
	Questions   []string `json:"questions,omitempty" jsonschema:"可执行的关键研究问题，最多 10 个"`
	Gap         string   `json:"gap,omitempty" jsonschema:"待验证的研究空白或假设，不把提案当成已证明结论"`
	Feasibility string   `json:"feasibility,omitempty" jsonschema:"数据、算力和时间约束下的可行性"`
}

type Direction struct {
	DirectionDraft
	ID                   string `json:"id"`
	ProjectID            string `json:"project_id"`
	State                string `json:"state"`
	Revision             int64  `json:"revision"`
	SourceSessionID      string `json:"source_session_id,omitempty"`
	SourceTurnID         string `json:"source_turn_id,omitempty"`
	EvidenceInsufficient bool   `json:"evidence_insufficient"`
}

type DirectionRepository interface {
	SaveDirection(context.Context, Scope, Direction, int64, string) (Direction, error)
	GetDirection(context.Context, Scope, string) (Direction, error)
	ListDirections(context.Context, Scope) ([]Direction, error)
	DirectionHistory(context.Context, Scope, string) ([]Direction, error)
}
