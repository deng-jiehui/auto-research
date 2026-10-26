package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/goharness/goharness/pkg/tool"
)

// ProjectResources 在可信 Scope 下生成只读资料；逻辑地址永不进入 os.Open。
type ProjectResources struct {
	ResearchTurnID string
	Scope          domain.Scope
	Projects       domain.ProjectRepository
	Directions     *DirectionService
	Research       *ResearchService
	Papers         *PaperService
	Documents      *DocumentService
}

type reportEvidenceView struct {
	domain.Evidence
	CitationURL string `json:"citation_url"`
	SourcePath  string `json:"source_path"`
}

func (r *ProjectResources) researchScope(ctx context.Context) (*domain.ResearchRun, error) {
	if r.ResearchTurnID == "" || r.Research == nil {
		return nil, nil
	}
	run, err := r.Research.ForTurn(ctx, r.Scope, r.ResearchTurnID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (r *ProjectResources) authorizePaper(ctx context.Context, paperID string) error {
	run, err := r.researchScope(ctx)
	if err != nil || run == nil {
		return err
	}
	if !researchSelectedPaper(*run, paperID) {
		return fmt.Errorf("%w：文件资料所属论文尚未被本任务纳入", domain.ErrConflict)
	}
	return nil
}

func (r *ProjectResources) visiblePapers(ctx context.Context, papers []domain.Paper) ([]domain.Paper, error) {
	run, err := r.researchScope(ctx)
	if err != nil || run == nil {
		return papers, err
	}
	return selectedResearchPapers(ctx, r.Scope, r.Papers, *run, domain.PaperQuery{Limit: 100})
}

func resourceAddress(address string) (string, url.Values, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "research" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return "", nil, domain.ErrInvalid
	}
	name := u.Host + u.Path
	for _, segment := range strings.Split(name, "/") {
		if segment == "." || segment == ".." || strings.ContainsAny(segment, "\\\x00") {
			return "", nil, domain.ErrInvalid
		}
	}
	for key, values := range u.Query() {
		if (key != "offset" && key != "revision") || len(values) != 1 {
			return "", nil, domain.ErrInvalid
		}
	}
	return name, u.Query(), nil
}

func resourceJSON(value any) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	return string(data) + "\n", err
}

func resourcePage(address string, query url.Values, items []any, binding ...string) (string, error) {
	all, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	revision := sha256Hex(append(all, []byte(strings.Join(binding, ":"))...))
	if expected := query.Get("revision"); expected != "" && expected != revision {
		return "", fmt.Errorf("%w：资料已更新，请从第一页重新读取", domain.ErrConflict)
	}
	offset := 0
	if raw := query.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > len(items) {
			return "", domain.ErrInvalid
		}
	}
	if offset > 0 && query.Get("revision") == "" {
		return "", fmt.Errorf("%w：后续页必须携带 revision", domain.ErrInvalid)
	}
	end := min(offset+20, len(items))
	next := ""
	if end < len(items) {
		next = fmt.Sprintf("research://%s?offset=%d&revision=%s", address, end, revision)
	}
	return resourceJSON(map[string]any{"revision": revision, "items": items[offset:end], "next": next, "total": len(items)})
}

func (r *ProjectResources) content(ctx context.Context, address string) (string, error) {
	name, query, err := resourceAddress(address)
	if err != nil {
		return "", err
	}
	if _, err = r.Projects.GetProject(ctx, r.Scope); err != nil {
		return "", err
	}
	switch name {
	case "index.json":
		index := map[string]any{"project_id": r.Scope.ProjectID, "directions": "research://directions/index.json", "papers": "research://papers/index.json", "usage": "逻辑资料只读；论文证据通过 read_paper、search_project_evidence 与 save_evidence 核验。"}
		if r.Research != nil {
			index["runs"] = "research://runs/index.json"
		}
		return resourceJSON(index)
	case "runs/index.json":
		if r.Research == nil {
			return "", domain.ErrUnavailable
		}
		list, err := r.Research.List(ctx, r.Scope)
		if err != nil {
			return "", err
		}
		items := make([]any, 0, len(list))
		for _, run := range list {
			items = append(items, map[string]any{"id": run.ID, "question": run.Spec.Question, "phase": run.Phase, "plan_revision": run.PlanRevision, "path": "research://runs/" + run.ID + ".json"})
		}
		return resourcePage(name, query, items)
	case "directions/index.json":
		if r.Directions == nil {
			return "", domain.ErrUnavailable
		}
		list, err := r.Directions.List(ctx, r.Scope)
		if err != nil {
			return "", err
		}
		items := make([]any, 0, len(list))
		for _, item := range list {
			items = append(items, map[string]any{"id": item.ID, "name": item.Name, "state": item.State, "revision": item.Revision, "path": "research://directions/" + item.ID + ".json"})
		}
		return resourcePage(name, query, items)
	case "papers/index.json":
		if r.Papers == nil {
			return "", domain.ErrUnavailable
		}
		list, err := r.Papers.List(ctx, r.Scope, domain.PaperQuery{Limit: 100})
		if err != nil {
			return "", err
		}
		list, err = r.visiblePapers(ctx, list)
		if err != nil {
			return "", err
		}
		items := make([]any, 0, len(list))
		for _, item := range list {
			items = append(items, map[string]any{"id": item.ID, "title": item.Title, "revision": item.Revision, "path": "research://papers/" + item.ID + ".json"})
		}
		return resourcePage(name, query, items)
	}
	parts := strings.Split(name, "/")
	if len(parts) == 3 && parts[0] == "reports" && (parts[2] == "report.md" || parts[2] == "matrix.json" || parts[2] == "references.json") && r.Research != nil {
		report, err := r.Research.Report(ctx, r.Scope, parts[1])
		if err != nil {
			return "", err
		}
		if parts[2] == "report.md" {
			return report.Markdown, nil
		}
		items := []any{}
		if parts[2] == "matrix.json" {
			for _, row := range report.Matrix.Rows {
				items = append(items, row)
			}
		} else {
			for _, evidence := range report.References {
				sourcePath := "research://documents/" + evidence.DocumentID + "/contents.json"
				if evidence.BlockID != "" {
					sourcePath = "research://documents/" + evidence.DocumentID + "/blocks/" + evidence.BlockID + ".txt"
				}
				items = append(items, reportEvidenceView{Evidence: evidence,
					CitationURL: "/p/" + r.Scope.ProjectID + "/papers?paper=" + evidence.PaperID + "&evidence=" + evidence.ID,
					SourcePath:  sourcePath})
			}
		}
		page, err := resourcePage(name, query, items, report.ID, report.MatrixHash)
		if err != nil {
			return "", err
		}
		var result map[string]any
		if err := json.Unmarshal([]byte(page), &result); err != nil {
			return "", err
		}
		result["report_id"], result["run_id"], result["report_revision"], result["matrix_hash"] = report.ID, report.RunID, report.Revision, report.MatrixHash
		return resourceJSON(result)
	}
	if len(parts) == 3 && parts[0] == "runs" && (parts[2] == "matrix.json" || parts[2] == "reports.json") && r.Research != nil {
		if parts[2] == "reports.json" {
			reports, err := r.Research.Reports(ctx, r.Scope, parts[1])
			if err != nil {
				return "", err
			}
			items := make([]any, 0, len(reports))
			for _, report := range reports {
				items = append(items, report.ReportReference)
			}
			return resourcePage(name, query, items)
		}
		matrix, err := r.Research.Matrix(ctx, r.Scope, parts[1])
		if err != nil {
			return "", err
		}
		items := make([]any, 0, len(matrix.Rows))
		for _, row := range matrix.Rows {
			items = append(items, row)
		}
		page, err := resourcePage(name, query, items, matrix.Hash)
		if err != nil {
			return "", err
		}
		var result map[string]any
		if err = json.Unmarshal([]byte(page), &result); err != nil {
			return "", err
		}
		result["hash"], result["run_id"] = matrix.Hash, matrix.RunID
		return resourceJSON(result)
	}
	if len(parts) == 3 && parts[0] == "runs" && parts[2] == "candidates.json" && r.Research != nil {
		run, err := r.Research.Get(ctx, r.Scope, parts[1])
		if err != nil {
			return "", err
		}
		items := make([]any, 0, len(run.Candidates))
		for _, c := range run.Candidates {
			items = append(items, c)
		}
		// resourcePage 的指纹绑定整套内容；每页另外返回业务审核版本。
		page, err := resourcePage(name, query, items, fmt.Sprintf("%d:%s", run.CandidateRevision, run.CandidateHash))
		if err != nil {
			return "", err
		}
		var result map[string]any
		if err = json.Unmarshal([]byte(page), &result); err != nil {
			return "", err
		}
		result["candidate_revision"], result["candidate_hash"], result["round"] = run.CandidateRevision, run.CandidateHash, run.CandidateRound
		return resourceJSON(result)
	}
	if len(parts) == 2 && parts[0] == "runs" && strings.HasSuffix(parts[1], ".json") && r.Research != nil {
		run, err := r.Research.Get(ctx, r.Scope, strings.TrimSuffix(parts[1], ".json"))
		if err != nil {
			return "", err
		}
		return resourceJSON(run)
	}
	if len(parts) == 2 && parts[0] == "directions" && strings.HasSuffix(parts[1], ".json") && r.Directions != nil {
		item, err := r.Directions.Get(ctx, r.Scope, strings.TrimSuffix(parts[1], ".json"))
		if err != nil {
			return "", err
		}
		return resourceJSON(item)
	}
	if len(parts) == 2 && parts[0] == "papers" && strings.HasSuffix(parts[1], ".json") && r.Papers != nil {
		if err = r.authorizePaper(ctx, strings.TrimSuffix(parts[1], ".json")); err != nil {
			return "", err
		}
		item, err := r.Papers.Get(ctx, r.Scope, strings.TrimSuffix(parts[1], ".json"))
		if err != nil {
			return "", err
		}
		return resourceJSON(item)
	}
	if len(parts) == 2 && parts[0] == "evidence" && strings.HasSuffix(parts[1], ".json") && r.Documents != nil {
		item, err := r.Documents.Evidence(ctx, r.Scope, strings.TrimSuffix(parts[1], ".json"))
		if err != nil {
			return "", err
		}
		if err = r.authorizePaper(ctx, item.PaperID); err != nil {
			return "", err
		}
		return resourceJSON(item)
	}
	if len(parts) == 3 && parts[0] == "documents" && parts[2] == "contents.json" && r.Documents != nil {
		item, err := r.Documents.Contents(ctx, r.Scope, parts[1])
		if err != nil {
			return "", err
		}
		if err = r.authorizePaper(ctx, item.PaperID); err != nil {
			return "", err
		}
		return resourceJSON(item)
	}
	if len(parts) == 4 && parts[0] == "documents" && parts[2] == "blocks" && strings.HasSuffix(parts[3], ".txt") && r.Documents != nil {
		doc, err := r.Documents.Contents(ctx, r.Scope, parts[1])
		if err != nil {
			return "", err
		}
		if err = r.authorizePaper(ctx, doc.PaperID); err != nil {
			return "", err
		}
		item, err := r.Documents.Read(ctx, r.Scope, parts[1], strings.TrimSuffix(parts[3], ".txt"))
		return item.Text, err
	}
	return "", domain.ErrNotFound
}

func (r *ProjectResources) Read(ctx context.Context, address string, input ReadFileArgs) (ReadFileResult, error) {
	text, err := r.content(ctx, address)
	if err != nil {
		return ReadFileResult{}, err
	}
	if name, query, parseErr := resourceAddress(address); parseErr == nil && !strings.HasSuffix(name, "index.json") && !strings.HasSuffix(name, "/candidates.json") && !strings.HasSuffix(name, "/matrix.json") && !strings.HasSuffix(name, "/references.json") && !strings.HasSuffix(name, "/reports.json") && query.Get("revision") != "" && query.Get("revision") != sha256Hex([]byte(text)) {
		return ReadFileResult{}, domain.ErrConflict
	}
	lines := strings.SplitAfter(text, "\n")
	start, end := input.StartLine, input.EndLine
	if start <= 0 {
		start = 1
	}
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return ReadFileResult{}, domain.ErrInvalid
	}
	content := strings.Join(lines[start-1:end], "")
	result := ReadFileResult{Path: address, Content: content, StartLine: start, EndLine: end, SHA256: sha256Hex([]byte(text)), Truncated: end < len(lines)}
	if len(result.Content) > 32<<10 {
		result.Content = result.Content[:32<<10]
		for !utf8.ValidString(result.Content) {
			result.Content = result.Content[:len(result.Content)-1]
		}
		// 单行也有边界，不能返回错误的下一行来悄悄遗漏未读取内容。
		lastBreak := strings.LastIndex(result.Content, "\n")
		if lastBreak < 0 {
			return ReadFileResult{}, fmt.Errorf("%w：资料单行过长，请读取更具体的对象", domain.ErrInvalid)
		}
		result.Content = result.Content[:lastBreak+1]
		result.EndLine = start + strings.Count(result.Content, "\n") - 1
		result.Truncated = true
	}
	if result.Truncated {
		result.NextHint = fmt.Sprintf("继续 read_file path=%s start_line=%d；核对 SHA256=%s，内容变化后重新读取", address, result.EndLine+1, result.SHA256)
	}
	return result, nil
}

func (r *ProjectResources) catalog(ctx context.Context, prefix string) ([]WorkspaceFileEntry, error) {
	if _, _, err := resourceAddress(prefix); err != nil {
		return nil, err
	}
	if _, err := r.Projects.GetProject(ctx, r.Scope); err != nil {
		return nil, err
	}
	items := []WorkspaceFileEntry{{Path: "research://index.json", Kind: "file"}}
	if r.Research != nil {
		items = append(items, WorkspaceFileEntry{Path: "research://runs/index.json", Kind: "file"})
		runs, err := r.Research.List(ctx, r.Scope)
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			items = append(items, WorkspaceFileEntry{Path: "research://runs/" + run.ID + ".json", Kind: "file"})
			items = append(items, WorkspaceFileEntry{Path: "research://runs/" + run.ID + "/candidates.json", Kind: "file"})
			items = append(items, WorkspaceFileEntry{Path: "research://runs/" + run.ID + "/matrix.json", Kind: "file"}, WorkspaceFileEntry{Path: "research://runs/" + run.ID + "/reports.json", Kind: "file"})
			reports, err := r.Research.Reports(ctx, r.Scope, run.ID)
			if err != nil {
				return nil, err
			}
			for _, report := range reports {
				items = append(items, WorkspaceFileEntry{Path: report.ResourcePath, Kind: "file"})
				root := "research://reports/" + report.ID + "/"
				items = append(items, WorkspaceFileEntry{Path: root + "matrix.json", Kind: "file"}, WorkspaceFileEntry{Path: root + "references.json", Kind: "file"})
			}
		}
	}
	if r.Directions != nil {
		items = append(items, WorkspaceFileEntry{Path: "research://directions/index.json", Kind: "file"})
		list, err := r.Directions.List(ctx, r.Scope)
		if err != nil {
			return nil, err
		}
		for _, item := range list {
			items = append(items, WorkspaceFileEntry{Path: "research://directions/" + item.ID + ".json", Kind: "file"})
		}
	}
	if r.Papers != nil {
		items = append(items, WorkspaceFileEntry{Path: "research://papers/index.json", Kind: "file"})
		papers, err := r.Papers.List(ctx, r.Scope, domain.PaperQuery{Limit: 100})
		if err != nil {
			return nil, err
		}
		papers, err = r.visiblePapers(ctx, papers)
		if err != nil {
			return nil, err
		}
		for _, paper := range papers {
			items = append(items, WorkspaceFileEntry{Path: "research://papers/" + paper.ID + ".json", Kind: "file"})
		}
	}
	var filtered []WorkspaceFileEntry
	for _, item := range items {
		if prefix == "research://" || item.Path == prefix || strings.HasPrefix(item.Path, strings.TrimRight(prefix, "/")+"/") {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

// 单独参数类型使新图广告逻辑资料能力，旧图继续使用原输入 schema。
type resourceReadArgs struct {
	Path      string `json:"path" jsonschema:"工作区相对文件路径或只读 research:// 资料地址；先读取 research://index.json"`
	StartLine int    `json:"start_line,omitempty" jsonschema:"起始行，从 1 开始"`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"结束行，默认末尾；超过返回上限需分段读取"`
}
type resourceListArgs struct {
	Path  string `json:"path,omitempty" jsonschema:"工作区相对目录或 research://、research://directions 等逻辑目录"`
	Depth int    `json:"depth,omitempty" jsonschema:"本地目录递归深度，默认 2；逻辑目录返回可读地址"`
}
type resourceSearchArgs struct {
	Query      string `json:"query" jsonschema:"搜索文本或正则表达式"`
	Path       string `json:"path,omitempty" jsonschema:"工作区相对路径或 research:// 逻辑资料范围；身份由应用注入"`
	Glob       string `json:"glob,omitempty" jsonschema:"文件名匹配，如 *.json"`
	Regex      bool   `json:"regex,omitempty" jsonschema:"是否使用正则表达式"`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"最多返回条数，最大 200"`
}

func projectReadTools(policy WorkspacePolicy, resources *ProjectResources) []tool.Tool {
	return []tool.Tool{
		tool.New[resourceListArgs, ListDirectoryResult]("list_directory", "浏览工作区或当前项目 research:// 资料目录；逻辑资料只读，返回可直接 read_file 的地址。", func(ctx context.Context, input resourceListArgs) (ListDirectoryResult, error) {
			if !strings.HasPrefix(input.Path, "research:") {
				return listWorkspaceDirectory(policy, ListDirectoryArgs(input))
			}
			items, err := resources.catalog(ctx, input.Path)
			truncated := len(items) > workspaceMaxListEntries
			if truncated {
				items = items[:workspaceMaxListEntries]
			}
			return ListDirectoryResult{Entries: items, Truncated: truncated, NextHint: "已知资料地址可直接 read_file；大列表按 index.json 的 next 读取"}, err
		}, tool.ReadOnly()),
		tool.New[resourceReadArgs, ReadFileResult]("read_file", "按行读取工作区文本或当前项目 research:// 只读资料。先读 research://index.json；返回完整内容哈希、截断提示和可继续读取的地址，不允许扩大项目范围。", func(ctx context.Context, input resourceReadArgs) (ReadFileResult, error) {
			if strings.HasPrefix(input.Path, "research:") {
				return resources.Read(ctx, input.Path, ReadFileArgs(input))
			}
			return readWorkspaceFile(policy, ReadFileArgs(input))
		}, tool.ReadOnly()),
		tool.New[resourceSearchArgs, SearchFilesResult]("search_files", "搜索工作区或当前项目 research:// 资料；返回可读取的路径、行号及片段。精确原文证据检索使用 search_project_evidence。", func(ctx context.Context, input resourceSearchArgs) (SearchFilesResult, error) {
			if !strings.HasPrefix(input.Path, "research:") {
				return searchWorkspaceFiles(policy, SearchFilesArgs(input))
			}
			if strings.TrimSpace(input.Query) == "" || len(input.Query) > 1000 || input.MaxResults < 0 || input.MaxResults > 200 {
				return SearchFilesResult{}, domain.ErrInvalid
			}
			limit := input.MaxResults
			if limit == 0 {
				limit = 30
			}
			var expression *regexp.Regexp
			var err error
			if input.Regex {
				expression, err = regexp.Compile(input.Query)
				if err != nil {
					return SearchFilesResult{}, domain.ErrInvalid
				}
			}
			items, err := resources.catalog(ctx, input.Path)
			if err != nil {
				return SearchFilesResult{}, err
			}
			result := SearchFilesResult{Matches: make([]SearchMatch, 0)}
			for i, item := range items {
				if i >= workspaceMaxListEntries {
					result.Truncated = true
					break
				}
				if input.Glob != "" {
					ok, globErr := path.Match(input.Glob, path.Base(item.Path))
					if globErr != nil {
						return result, domain.ErrInvalid
					}
					if !ok {
						continue
					}
				}
				text, readErr := resources.content(ctx, item.Path)
				if readErr != nil {
					return result, readErr
				}
				for line, value := range strings.Split(text, "\n") {
					match := strings.Contains(strings.ToLower(value), strings.ToLower(input.Query))
					if expression != nil {
						match = expression.MatchString(value)
					}
					if match {
						if len(result.Matches) >= limit {
							result.Truncated = true
							return result, nil
						}
						result.Matches = append(result.Matches, SearchMatch{Path: item.Path, Line: line + 1, Text: truncateRunes(value, 400)})
					}
				}
			}
			return result, nil
		}, tool.ReadOnly()),
	}
}
