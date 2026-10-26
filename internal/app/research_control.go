package app

import (
	"context"
	"encoding/json"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

type ResearchControlInput struct {
	TurnActionInput
	PaperID      string `json:"paper_id,omitempty"`
	WorkRevision int64  `json:"work_revision,omitempty"`
}

// 已完成研究的再次执行、以及冻结输入的编辑，均保留旧成果并派生新任务。
func (s *TurnService) forkResearchTurn(ctx context.Context, scope domain.Scope, turn domain.Turn, run domain.ResearchRun, kind string, in TurnActionInput, text string) (TurnActionResult, error) {
	if turn.Active() {
		return TurnActionResult{}, domain.ErrBusy
	}
	if turn.Revision != in.Revision {
		return TurnActionResult{}, domain.ErrConflict
	}
	spec := run.Spec
	spec.Question = text
	var tc turnContext
	if err := json.Unmarshal(turn.ContextJSON, &tc); err != nil {
		return TurnActionResult{}, domain.ErrInvalid
	}
	key := "research-fork:" + hash([]byte(run.ID+":"+kind+":"+in.IdempotencyKey))
	if run.Detached {
		created, err := s.StartResearch(ctx, scope, StartResearchInput{ResearchSpec: spec, OriginSessionID: run.OriginSessionID, OriginTurnID: run.OriginTurnID, IdempotencyKey: key, previousRunID: run.ID})
		if err != nil {
			return TurnActionResult{}, err
		}
		return TurnActionResult{Turn: created.Turn, Replayed: created.Replayed}, nil
	}
	created, err := s.Send(ctx, scope, run.SessionID, SendInput{Text: text, IdempotencyKey: key, AgentMode: tc.AgentMode, Research: &spec, previousResearchRunID: run.ID})
	if err != nil {
		return TurnActionResult{}, err
	}
	return TurnActionResult{Turn: created.Turn, Replayed: created.Replayed}, nil
}

func (s *TurnService) ControlResearch(ctx context.Context, scope domain.Scope, runID, kind string, in ResearchControlInput) (TurnActionResult, error) {
	if in.Revision < 1 || in.IdempotencyKey == "" || (kind != "pause" && kind != "continue" && kind != "retry" && kind != "cancel") {
		return TurnActionResult{}, domain.ErrInvalid
	}
	if s.research == nil {
		return TurnActionResult{}, domain.ErrUnavailable
	}
	run, err := s.research.Get(ctx, scope, runID)
	if err != nil {
		return TurnActionResult{}, err
	}
	if kind == "cancel" {
		return s.Cancel(ctx, scope, run.SessionID, run.TurnID, in.TurnActionInput)
	}
	payload, _ := json.Marshal(struct {
		PaperID      string `json:"paper_id,omitempty"`
		WorkRevision int64  `json:"work_revision,omitempty"`
	}{in.PaperID, in.WorkRevision})
	result, err := s.runtime.ApplyAction(ctx, scope, run.SessionID, run.TurnID, domain.ActionDraft{Kind: "research_" + kind, IdempotencyKey: in.IdempotencyKey, Revision: in.Revision, RequestHash: hash(payload), PayloadJSON: payload})
	if err != nil {
		return TurnActionResult{}, err
	}
	s.signalWake()
	return TurnActionResult{Turn: result.Turn, ActionID: result.Action.ID, Replayed: result.Replayed}, nil
}
