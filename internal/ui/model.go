// Package ui 提供端无关的展示与用户交互协议，不持有执行状态。
package ui

import (
	"encoding/json"
	"strings"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

// Target 使用用户可见的会话或研究身份，不要求研究调用方操作内部会话。
type Target struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type Interaction struct {
	ID           string               `json:"id"`
	Kind         string               `json:"kind"`
	Question     string               `json:"question"`
	Choices      []string             `json:"choices,omitempty"`
	AllowCustom  bool                 `json:"allow_custom"`
	Placeholder  string               `json:"placeholder,omitempty"`
	RunID        string               `json:"run_id,omitempty"`
	Revision     int64                `json:"revision,omitempty"`
	Hash         string               `json:"hash,omitempty"`
	Plan         *domain.ResearchPlan `json:"plan,omitempty"`
	CandidateIDs []string             `json:"candidate_ids,omitempty"`
	AnalyzeLimit int                  `json:"analyze_limit,omitempty"`
}

// State 是展示投影；动作列表只作界面提示，授权与版本校验仍由应用层执行。
type State struct {
	Version          string       `json:"version"`
	Target           Target       `json:"target"`
	EventCursor      int64        `json:"event_cursor"`
	Interaction      *Interaction `json:"interaction"`
	InteractionError string       `json:"interaction_error,omitempty"`
	AvailableActions []string     `json:"available_actions"`
}

type View struct {
	domain.TurnSnapshot
	UI       State               `json:"ui"`
	Research *domain.ResearchRun `json:"research,omitempty"`
}

// Present 将持久状态转换成展示数据，旧检查点无需迁移或重新调用模型。
func Present(target Target, snapshot domain.TurnSnapshot, run *domain.ResearchRun) View {
	view := View{TurnSnapshot: snapshot, Research: run, UI: State{Version: "1", Target: target, AvailableActions: []string{}}}
	if run != nil && run.Detached {
		view.Notes = nil
	}
	for _, event := range snapshot.Events {
		if event.ID > view.UI.EventCursor {
			view.UI.EventCursor = event.ID
		}
	}
	turn := snapshot.Turn
	if turn.Active() {
		view.UI.AvailableActions = append(view.UI.AvailableActions, "cancel")
	}
	if turn.State == domain.TurnFailed || (run != nil && (turn.State == domain.TurnCompleted || turn.State == domain.TurnCancelled)) {
		view.UI.AvailableActions = append(view.UI.AvailableActions, "retry")
	}
	if run != nil && !run.PauseRequested && (turn.State == domain.TurnRunning || turn.State == domain.TurnQueued) && (run.Phase == "analyzing" || run.Phase == "reporting") {
		view.UI.AvailableActions = append(view.UI.AvailableActions, "pause")
	}
	if turn.State != domain.TurnAwaitingInput {
		return view
	}
	interaction, ok := presentInteraction(turn.PendingQuestions)
	if !ok {
		view.UI.InteractionError = "交互数据无法读取，请刷新页面；如仍无法恢复，请查看运行详情。"
		return view
	}
	if run != nil && interaction.RunID != "" && interaction.RunID != run.ID {
		view.UI.InteractionError = "交互与当前研究任务不匹配，请刷新后重试。"
		return view
	}
	view.UI.Interaction = interaction
	switch interaction.Kind {
	case "clarification", "research_plan", "research_candidates":
		view.UI.AvailableActions = append(view.UI.AvailableActions, "respond")
	case "research_pause":
		if run != nil {
			view.UI.AvailableActions = append(view.UI.AvailableActions, "continue")
		}
	}
	return view
}

func presentInteraction(raw []byte) (*Interaction, bool) {
	var pending []struct {
		ID      string          `json:"id"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(raw, &pending) != nil || len(pending) != 1 || strings.TrimSpace(pending[0].ID) == "" {
		return nil, false
	}
	payload := pending[0].Payload
	var text string
	if json.Unmarshal(payload, &text) == nil {
		payload = []byte(text)
	}
	var value struct {
		Kind         string               `json:"kind"`
		Question     string               `json:"question"`
		Choices      []json.RawMessage    `json:"choices"`
		AllowCustom  *bool                `json:"allow_custom"`
		Placeholder  string               `json:"placeholder"`
		RunID        string               `json:"run_id"`
		Revision     int64                `json:"revision"`
		Hash         string               `json:"hash"`
		Plan         *domain.ResearchPlan `json:"plan"`
		CandidateIDs []string             `json:"candidate_ids"`
		AnalyzeLimit int                  `json:"analyze_limit"`
	}
	if json.Unmarshal(payload, &value) != nil {
		return nil, false
	}
	question := strings.TrimSpace(value.Question)
	if question == "" {
		return nil, false
	}
	interaction := &Interaction{ID: pending[0].ID, Kind: value.Kind, Question: question, RunID: value.RunID, Revision: value.Revision, Hash: value.Hash, Plan: value.Plan, CandidateIDs: value.CandidateIDs, AnalyzeLimit: value.AnalyzeLimit}
	switch value.Kind {
	case "", "clarification":
		interaction.Kind = "clarification"
		interaction.AllowCustom = value.AllowCustom == nil || *value.AllowCustom
		interaction.Placeholder = strings.TrimSpace(value.Placeholder)
		if interaction.Placeholder == "" {
			interaction.Placeholder = "可以直接输入，或编辑/选择上方选项"
		}
		for _, rawChoice := range value.Choices {
			choice := choiceText(rawChoice)
			if choice != "" {
				interaction.Choices = append(interaction.Choices, choice)
			}
		}
	case "research_plan", "research_candidates":
		if value.RunID == "" || value.Revision < 1 || value.Hash == "" || (value.Kind == "research_plan" && value.Plan == nil) || (value.Kind == "research_candidates" && value.AnalyzeLimit < 1) {
			interaction.Kind = "unsupported"
		}
	case "research_pause":
		if value.RunID == "" {
			interaction.Kind = "unsupported"
		}
	default:
		interaction.Kind = "unsupported"
	}
	return interaction, true
}

func choiceText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return ""
	}
	for _, key := range []string{"label", "title", "text", "value"} {
		if json.Unmarshal(object[key], &text) == nil && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}
