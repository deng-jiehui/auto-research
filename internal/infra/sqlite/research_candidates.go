package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func researchOperation(ctx context.Context, q researchQuery, scope domain.Scope, turnID, operation, fingerprint string) (domain.ResearchRun, error) {
	if operation == "" || len(operation) > 1100 || fingerprint == "" {
		return domain.ResearchRun{}, domain.ErrInvalid
	}
	var previousHash, raw string
	err := q.QueryRowContext(ctx, `SELECT request_hash,result_json FROM research_operations WHERE owner_id=? AND project_id=? AND operation_id=?`, scope.UserID, scope.ProjectID, operation).Scan(&previousHash, &raw)
	if err != nil {
		return domain.ResearchRun{}, dbError(err)
	}
	var run domain.ResearchRun
	if err = json.Unmarshal([]byte(raw), &run); err != nil {
		return run, err
	}
	if previousHash != fingerprint || run.TurnID != turnID {
		return run, domain.ErrConflict
	}
	return run, nil
}

func saveResearchOperation(ctx context.Context, tx *sql.Tx, scope domain.Scope, operation, fingerprint string, run domain.ResearchRun) error {
	raw, err := json.Marshal(run)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO research_operations(owner_id,project_id,operation_id,request_hash,result_json) VALUES(?,?,?,?,?)`, scope.UserID, scope.ProjectID, operation, fingerprint, string(raw))
	return dbError(err)
}

func (s *Store) ResearchSearchReplay(ctx context.Context, scope domain.Scope, turnID, operation, fingerprint string) (domain.ResearchRun, error) {
	if operation == "" || len(operation) > 1024 {
		return domain.ResearchRun{}, domain.ErrInvalid
	}
	return researchOperation(ctx, s.db, scope, turnID, "search:"+operation, fingerprint)
}

// 每次来源响应只登记一次；重放先返回原结果，版本检查不能挡住已成功的请求。
func (s *Store) SaveResearchCandidates(ctx context.Context, scope domain.Scope, turnID, operation, fingerprint string, candidates []domain.Candidate) (domain.ResearchRun, error) {
	if operation == "" || len(operation) > 1024 {
		return domain.ResearchRun{}, domain.ErrInvalid
	}
	operation = "search:" + operation
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ResearchRun{}, dbError(err)
	}
	defer tx.Rollback()
	prior, err := researchOperation(ctx, tx, scope, turnID, operation, fingerprint)
	if err == nil || !errors.Is(err, domain.ErrNotFound) {
		return prior, err
	}
	run, err := researchForTurn(ctx, tx, scope, turnID)
	if err != nil {
		return run, err
	}
	if err = candidateWritable(ctx, tx, run); err != nil {
		return run, err
	}
	if run.CandidateRound == 0 {
		run.CandidateRound = 1
	}
	seen := make(map[string]bool)
	for _, c := range run.Candidates {
		seen[c.SourceKey] = true
	}
	for _, c := range candidates {
		key, err := domain.NormalizePaperKey(c.SourceKey)
		c.Title = strings.TrimSpace(c.Title)
		if err != nil || c.Title == "" || utf8.RuneCountInString(c.Title) > 500 {
			return run, domain.ErrInvalid
		}
		if seen[key] || len(run.Candidates) >= run.Spec.Settings.CandidateLimit {
			continue
		}
		if run.Spec.Settings.YearFrom > 0 && (c.Year < run.Spec.Settings.YearFrom || c.Year > run.Spec.Settings.YearTo) {
			continue
		}
		c.SourceKey = key
		if strings.HasPrefix(key, "arxiv:") && c.ArxivID == "" {
			c.ArxivID = strings.TrimPrefix(key, "arxiv:")
		}
		if strings.HasPrefix(key, "doi:") && c.DOI == "" {
			c.DOI = strings.TrimPrefix(key, "doi:")
		}
		identity := hashBytes([]byte(run.ID + ":" + key))
		run.Candidates = append(run.Candidates, domain.ResearchCandidate{Candidate: c, ID: identity[:32]})
		seen[key] = true
	}
	run.CandidateSearches++
	run.Phase = "searching"
	if err = putCandidateVersion(ctx, tx, scope, &run); err != nil {
		return run, err
	}
	if err = saveResearchOperation(ctx, tx, scope, operation, fingerprint, run); err != nil {
		return run, err
	}
	return run, dbError(tx.Commit())
}

func candidateWritable(ctx context.Context, tx *sql.Tx, run domain.ResearchRun) error {
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM turns WHERE id=?`, run.TurnID).Scan(&state); err != nil {
		return dbError(err)
	}
	if state != domain.TurnRunning && state != domain.TurnQueued {
		return domain.ErrConflict
	}
	if run.Phase == "cancelled" || run.PlanRevision == 0 || run.ApprovedPlanRevision != run.PlanRevision || run.ApprovedCandidateRevision != 0 {
		return fmt.Errorf("%w：计划尚未确认或候选已冻结", domain.ErrConflict)
	}
	return nil
}

func putCandidateVersion(ctx context.Context, tx *sql.Tx, scope domain.Scope, run *domain.ResearchRun) error {
	run.CandidateRevision++
	content, _ := json.Marshal(struct {
		PlanRevision    int64
		Round, Searches int
		Candidates      []domain.ResearchCandidate
	}{run.PlanRevision, run.CandidateRound, run.CandidateSearches, run.Candidates})
	run.CandidateHash = hashBytes(content)
	if err := putResearch(ctx, tx, scope, *run); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO research_candidate_versions(run_id,revision,snapshot_json) VALUES(?,?,?)`, run.ID, run.CandidateRevision, string(content))
	return dbError(err)
}

func (s *Store) PrepareCandidateReview(ctx context.Context, scope domain.Scope, turnID, operation string, expected int64, recommendations []domain.CandidateRecommendation) (domain.ResearchRun, error) {
	if operation == "" || len(operation) > 1024 {
		return domain.ResearchRun{}, domain.ErrInvalid
	}
	request, _ := json.Marshal(struct {
		TurnID          string
		Expected        int64
		Recommendations []domain.CandidateRecommendation
	}{turnID, expected, recommendations})
	fingerprint := hashBytes(request)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ResearchRun{}, dbError(err)
	}
	defer tx.Rollback()
	prior, err := researchOperation(ctx, tx, scope, turnID, "candidate-review:"+operation, fingerprint)
	if err == nil || !errors.Is(err, domain.ErrNotFound) {
		return prior, err
	}
	run, err := researchForTurn(ctx, tx, scope, turnID)
	if err != nil {
		return run, err
	}
	if err = candidateWritable(ctx, tx, run); err != nil {
		return run, err
	}
	if expected != run.CandidateRevision || run.CandidateSearches == 0 {
		return run, domain.ErrConflict
	}
	if len(recommendations) != len(run.Candidates) {
		return run, fmt.Errorf("%w：必须为每个候选提供推荐与理由", domain.ErrInvalid)
	}
	byID := make(map[string]domain.CandidateRecommendation)
	for _, r := range recommendations {
		if _, ok := byID[r.CandidateID]; ok || !validCandidateReason(r.Reason) {
			return run, domain.ErrInvalid
		}
		byID[r.CandidateID] = r
	}
	for i := range run.Candidates {
		r, ok := byID[run.Candidates[i].ID]
		if !ok {
			return run, fmt.Errorf("%w：推荐包含其他任务或未知候选", domain.ErrInvalid)
		}
		run.Candidates[i].Recommended, run.Candidates[i].RecommendationReason = r.Recommended, r.Reason
	}
	run.Phase = "candidate_review"
	if err = putCandidateVersion(ctx, tx, scope, &run); err != nil {
		return run, err
	}
	if err = saveResearchOperation(ctx, tx, scope, "candidate-review:"+operation, fingerprint, run); err != nil {
		return run, err
	}
	return run, dbError(tx.Commit())
}

func validCandidateReason(text string) bool {
	return strings.TrimSpace(text) != "" && utf8.RuneCountInString(text) <= 1000
}

func applyCandidateReview(ctx context.Context, tx *sql.Tx, scope domain.Scope, turn domain.Turn, review domain.ResearchCandidateReview, pendingRun string, revision int64, hash string) error {
	run, err := researchForTurn(ctx, tx, scope, turn.ID)
	if err != nil {
		return err
	}
	if run.ID != review.RunID || run.ID != pendingRun || run.CandidateRevision != review.CandidateRevision || revision != run.CandidateRevision || run.CandidateHash != review.CandidateHash || hash != run.CandidateHash || run.Phase != "candidate_review" || run.ApprovedCandidateRevision != 0 {
		return domain.ErrConflict
	}
	if review.Decision != "approve" && review.Decision != "search_again" && review.Decision != "cancel" {
		return domain.ErrInvalid
	}
	if utf8.RuneCountInString(review.Feedback) > 2000 {
		return domain.ErrInvalid
	}
	if review.Decision != "cancel" {
		if len(review.Choices) != len(run.Candidates) || review.AnalyzeLimit < 1 || review.AnalyzeLimit > run.Spec.Settings.AnalyzeLimit {
			return domain.ErrInvalid
		}
		byID := make(map[string]domain.ResearchCandidate)
		for _, c := range run.Candidates {
			byID[c.ID] = c
		}
		seen, selected := make(map[string]bool), 0
		for _, choice := range review.Choices {
			if _, ok := byID[choice.CandidateID]; !ok || seen[choice.CandidateID] || choice.PaperID != "" || !validCandidateReason(choice.Reason) {
				return domain.ErrInvalid
			}
			seen[choice.CandidateID] = true
			if choice.Selected {
				selected++
			}
		}
		if (review.Decision == "approve" && (selected == 0 || selected > review.AnalyzeLimit)) || (review.Decision == "search_again" && selected != 0) {
			return domain.ErrInvalid
		}
		if review.Decision == "approve" {
			for i := range review.Choices {
				choice := &review.Choices[i]
				if !choice.Selected {
					continue
				}
				c := byID[choice.CandidateID]
				paper, err := importPaper(ctx, tx, scope, domain.Paper{SourceKey: c.SourceKey, Title: c.Title, Authors: c.Authors, Year: c.Year, DOI: c.DOI, ArxivID: c.ArxivID, URL: c.URL, Abstract: c.Abstract, Tags: []string{}})
				if err != nil {
					return err
				}
				choice.PaperID = paper.ID
			}
			run.ApprovedCandidateRevision = run.CandidateRevision
			run.Phase = "candidates_selected"
			if strings.HasSuffix(turn.DefinitionVersion, "-v6") || (strings.HasSuffix(turn.DefinitionVersion, "-v7") || strings.HasSuffix(turn.DefinitionVersion, "-v8")) {
				if err = createResearchWorkItems(ctx, tx, run, review); err != nil {
					return err
				}
				run.Phase = "analyzing"
			}
		} else {
			run.Candidates = nil
			run.CandidateRound++
			run.CandidateSearches = 0
			run.Phase = "searching"
		}
	} else {
		run.Phase = "cancelled"
	}
	data, _ := json.Marshal(review)
	if _, err = tx.ExecContext(ctx, `INSERT INTO research_candidate_reviews(run_id,revision,review_json) VALUES(?,?,?)`, run.ID, review.CandidateRevision, string(data)); err != nil {
		return dbError(err)
	}
	run.CandidateReview = &review
	if review.Decision == "search_again" {
		return putCandidateVersion(ctx, tx, scope, &run)
	}
	return putResearch(ctx, tx, scope, run)
}
