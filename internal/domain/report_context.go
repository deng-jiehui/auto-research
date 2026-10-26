package domain

// ReportSelection 是消息的可选附件选择；省略表示继承，Clear 表示显式移除。
type ReportSelection struct {
	RunID    string `json:"run_id,omitempty"`
	ReportID string `json:"report_id,omitempty"`
	Clear    bool   `json:"clear,omitempty"`
}

// ReportContext 由服务端从已授权的不可变报告生成，不接受客户端自填标题或版本。
type ReportContext struct {
	RunID          string `json:"run_id"`
	ReportID       string `json:"report_id"`
	Revision       int64  `json:"revision"`
	Title          string `json:"title"`
	Summary        string `json:"summary"`
	MatrixHash     string `json:"matrix_hash"`
	ResourcePath   string `json:"resource_path"`
	MatrixPath     string `json:"matrix_path"`
	ReferencesPath string `json:"references_path"`
	ReportURL      string `json:"report_url"`
}

// ReportContextState 区分请求选择和解析结果，幂等重放只比较请求身份。
type ReportContextState struct {
	ReportSelection *ReportSelection `json:"report_selection,omitempty"`
	ReportContext   *ReportContext   `json:"report_context,omitempty"`
}
