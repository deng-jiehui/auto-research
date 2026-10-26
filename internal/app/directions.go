package app

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

type DirectionService struct {
	repo   domain.DirectionRepository
	papers domain.PaperRepository
}

func NewDirectionService(repo domain.DirectionRepository, papers domain.PaperRepository) *DirectionService {
	return &DirectionService{repo: repo, papers: papers}
}

func (s *DirectionService) validate(ctx context.Context, scope domain.Scope, draft domain.DirectionDraft) (domain.DirectionDraft, error) {
	draft.Name = strings.TrimSpace(draft.Name)
	draft.Description = strings.TrimSpace(draft.Description)
	if draft.Name == "" || draft.Description == "" || utf8.RuneCountInString(draft.Name) > 120 || utf8.RuneCountInString(draft.Description) > 4000 || len(draft.PaperIDs) > 30 || len(draft.Questions) > 10 || utf8.RuneCountInString(draft.Gap) > 4000 || utf8.RuneCountInString(draft.Feasibility) > 4000 {
		return draft, fmt.Errorf("%w：方向内容为空或超过长度/数量上限", domain.ErrInvalid)
	}
	for _, question := range draft.Questions {
		if strings.TrimSpace(question) == "" || utf8.RuneCountInString(question) > 1000 {
			return draft, fmt.Errorf("%w：关键问题为空或超过 1000 字符", domain.ErrInvalid)
		}
	}
	seen := map[string]bool{}
	for _, id := range draft.PaperIDs {
		if seen[id] {
			return draft, fmt.Errorf("%w：代表论文重复", domain.ErrInvalid)
		}
		seen[id] = true
		if _, err := s.papers.GetPaper(ctx, scope, id); err != nil {
			return draft, err
		}
	}
	return draft, nil
}

func (s *DirectionService) Propose(ctx context.Context, scope domain.Scope, draft domain.DirectionDraft, sessionID, turnID, key string) (domain.Direction, error) {
	draft, err := s.validate(ctx, scope, draft)
	if err != nil {
		return domain.Direction{}, err
	}
	return s.repo.SaveDirection(ctx, scope, domain.Direction{DirectionDraft: draft, ProjectID: scope.ProjectID, State: "proposed", SourceSessionID: sessionID, SourceTurnID: turnID, EvidenceInsufficient: len(draft.PaperIDs) == 0}, 0, key)
}
func (s *DirectionService) Edit(ctx context.Context, scope domain.Scope, id string, revision int64, draft domain.DirectionDraft) (domain.Direction, error) {
	current, err := s.Get(ctx, scope, id)
	if err != nil {
		return current, err
	}
	if current.Revision != revision {
		return domain.Direction{}, domain.ErrConflict
	}
	draft, err = s.validate(ctx, scope, draft)
	if err != nil {
		return domain.Direction{}, err
	}
	current.DirectionDraft, current.State, current.EvidenceInsufficient = draft, "proposed", len(draft.PaperIDs) == 0
	return s.repo.SaveDirection(ctx, scope, current, revision, "")
}
func (s *DirectionService) Decide(ctx context.Context, scope domain.Scope, id string, revision int64, state string) (domain.Direction, error) {
	if state != "accepted" && state != "rejected" && state != "archived" {
		return domain.Direction{}, domain.ErrInvalid
	}
	current, err := s.Get(ctx, scope, id)
	if err != nil {
		return current, err
	}
	if current.Revision != revision {
		return domain.Direction{}, domain.ErrConflict
	}
	if state != "archived" && current.State != "proposed" {
		return domain.Direction{}, fmt.Errorf("%w：只能接受或拒绝待确认提案", domain.ErrConflict)
	}
	current.State = state
	return s.repo.SaveDirection(ctx, scope, current, revision, "")
}
func (s *DirectionService) Get(ctx context.Context, scope domain.Scope, id string) (domain.Direction, error) {
	return s.repo.GetDirection(ctx, scope, id)
}
func (s *DirectionService) List(ctx context.Context, scope domain.Scope) ([]domain.Direction, error) {
	return s.repo.ListDirections(ctx, scope)
}
func (s *DirectionService) History(ctx context.Context, scope domain.Scope, id string) ([]domain.Direction, error) {
	return s.repo.DirectionHistory(ctx, scope, id)
}
