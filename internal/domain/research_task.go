package domain

// ResearchTaskDraft 由应用构造，来源身份与内部上下文不能由模型伪造。
type ResearchTaskDraft struct {
	Spec              ResearchSpec
	OriginSessionID   string
	OriginTurnID      string
	PreviousRunID     string
	OperationID       string
	ContextJSON       []byte
	MessageJSON       []byte
	DefinitionVersion string
}

type ResearchTaskCreated struct {
	Run      ResearchRun `json:"run"`
	Turn     Turn        `json:"turn"`
	Replayed bool        `json:"replayed"`
}

// ResearchTaskSummary 是会话唯一自动接收的研究投影，不含研究过程与完整成果。
type ResearchTaskSummary struct {
	ReportPath   string `json:"report_path,omitempty"`
	ID           string `json:"id"`
	Question     string `json:"question"`
	State        string `json:"state"`
	Phase        string `json:"phase"`
	Summary      string `json:"summary"`
	TaskURL      string `json:"task_url"`
	ResourcePath string `json:"resource_path"`
}
