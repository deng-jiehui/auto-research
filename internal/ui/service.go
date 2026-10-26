package ui

import (
	"context"
	"strings"

	"github.com/deng-jiehui/auto-research/internal/app"
	"github.com/deng-jiehui/auto-research/internal/domain"
)

// Service 被 HTTP 与未来终端适配器共用；执行、预算和审批规则仍属于应用服务。
type Service struct{ turns *app.TurnService }

func NewService(turns *app.TurnService) *Service { return &Service{turns: turns} }

func (s *Service) resolve(ctx context.Context, scope domain.Scope, target Target, turnID string) (string, string, *domain.ResearchRun, error) {
	if s == nil || s.turns == nil {
		return "", "", nil, domain.ErrUnavailable
	}
	if strings.TrimSpace(target.ID) == "" {
		return "", "", nil, domain.ErrInvalid
	}
	switch target.Kind {
	case "session":
		return target.ID, turnID, nil, nil
	case "research":
		if s.turns.ResearchService() == nil {
			return "", "", nil, domain.ErrUnavailable
		}
		run, err := s.turns.ResearchService().Get(ctx, scope, target.ID)
		if err != nil {
			return "", "", nil, err
		}
		if turnID != "" && turnID != run.TurnID {
			return "", "", nil, domain.ErrConflict
		}
		return run.SessionID, run.TurnID, &run, nil
	default:
		return "", "", nil, domain.ErrInvalid
	}
}

func (s *Service) Snapshot(ctx context.Context, scope domain.Scope, target Target, limit int) (View, error) {
	sessionID, _, run, err := s.resolve(ctx, scope, target, "")
	if err != nil {
		return View{}, err
	}
	snapshot, err := s.turns.Snapshot(ctx, scope, sessionID, limit)
	if err != nil {
		return View{}, err
	}
	if run != nil && snapshot.Turn.ID != run.TurnID {
		return View{}, domain.ErrConflict
	}
	return Present(target, snapshot, run), nil
}

func (s *Service) Events(ctx context.Context, scope domain.Scope, target Target, after int64, limit int) ([]domain.SessionEvent, error) {
	if after < 0 {
		return nil, domain.ErrInvalid
	}
	sessionID, _, _, err := s.resolve(ctx, scope, target, "")
	if err != nil {
		return nil, err
	}
	return s.turns.Events(ctx, scope, sessionID, after, limit)
}

func (s *Service) Respond(ctx context.Context, scope domain.Scope, target Target, turnID string, in app.ResumeInput) (app.TurnActionResult, error) {
	sessionID, boundTurn, _, err := s.resolve(ctx, scope, target, turnID)
	if err != nil {
		return app.TurnActionResult{}, err
	}
	if boundTurn == "" {
		return app.TurnActionResult{}, domain.ErrInvalid
	}
	// 不先检查展示状态，保证成功回答即使已结束等待仍能幂等重放。
	return s.turns.Resume(ctx, scope, sessionID, boundTurn, in)
}

func (s *Service) Act(ctx context.Context, scope domain.Scope, target Target, turnID, kind string, in app.ResearchControlInput) (app.TurnActionResult, error) {
	sessionID, boundTurn, run, err := s.resolve(ctx, scope, target, turnID)
	if err != nil {
		return app.TurnActionResult{}, err
	}
	if boundTurn == "" {
		return app.TurnActionResult{}, domain.ErrInvalid
	}
	if kind == "pause" || kind == "continue" || (kind == "retry" && (in.PaperID != "" || in.WorkRevision != 0)) {
		if run == nil {
			return app.TurnActionResult{}, domain.ErrInvalid
		}
		return s.turns.ControlResearch(ctx, scope, run.ID, kind, in)
	}
	if in.PaperID != "" || in.WorkRevision != 0 {
		return app.TurnActionResult{}, domain.ErrInvalid
	}
	switch kind {
	case "retry":
		return s.turns.Retry(ctx, scope, sessionID, boundTurn, in.TurnActionInput)
	case "cancel":
		return s.turns.Cancel(ctx, scope, sessionID, boundTurn, in.TurnActionInput)
	default:
		return app.TurnActionResult{}, domain.ErrInvalid
	}
}
