package domain

type Session struct {
	Kind      string `json:"kind,omitempty"`
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Title     string `json:"title"`
	Archived  bool   `json:"archived"`
}
