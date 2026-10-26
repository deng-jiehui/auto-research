package app

import (
	"context"
	"fmt"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/goharness/goharness/pkg/tool"
)

type ReadPaperDocumentArgs struct {
	PaperID    string `json:"paper_id" jsonschema:"本项目已归档论文 ID，来自 save_paper 或 list_papers"`
	Section    string `json:"section,omitempty" jsonschema:"abstract 默认读取摘要；fulltext 获取或复用全文并返回目录；contents 读取已知文档目录；block 按块读取正文"`
	DocumentID string `json:"document_id,omitempty" jsonschema:"contents/block 时必填，必须来自本论文工具返回的文档 ID"`
	BlockID    string `json:"block_id,omitempty" jsonschema:"block 时必填，来自全文目录的块 ID，例如 b2"`
	Refresh    bool   `json:"refresh,omitempty" jsonschema:"仅 fulltext 支持；用户明确要求重新获取来源版本时设为 true，否则复用冻结全文"`
}
type EvidenceSearchArgs struct {
	Query string `json:"query" jsonschema:"当前项目论文中的英文词项或中文原文短语，最多 500 字符"`
	Limit int    `json:"limit,omitempty" jsonschema:"最多返回的证据块数，默认 10，最大 30"`
}
type SaveEvidenceArgs struct {
	DocumentID string `json:"document_id" jsonschema:"可信阅读或检索工具返回的冻结文档 ID"`
	BlockID    string `json:"block_id" jsonschema:"可信全文目录或检索返回的块 ID"`
	Quote      string `json:"quote" jsonschema:"该块中逐字引用的唯一原文，不改写、不估算字符区间"`
	Field      string `json:"field,omitempty" jsonschema:"证据字段，例如 method、dataset、metrics、limitation"`
	Value      string `json:"value,omitempty" jsonschema:"该字段的归纳说明，须有 Quote 支撑"`
}
type EvidenceToolResult struct {
	domain.Evidence
	CitationURL string `json:"citation_url"`
}

func newDocumentReadTool(deps AcademicToolsDeps) tool.Tool {
	return newAcademicTool[ReadPaperDocumentArgs, any]("read_paper", "阅读本项目论文。默认返回真实摘要；section=fulltext 获取/复用全文并仅返回目录，随后 section=block 按 document_id/block_id 阅读，避免一次读取整篇。只有已读片段才可作为证据。下载或解析失败明确报错，不将摘要冒充全文。", func(ctx context.Context, args ReadPaperDocumentArgs) (any, error) {
		paper, err := deps.Papers.Get(ctx, deps.Scope, args.PaperID)
		if err != nil {
			return nil, err
		}
		if args.Section != "fulltext" && args.Refresh {
			return nil, domain.ErrInvalid
		}
		switch args.Section {
		case "", "abstract":
			doc, err := deps.Academic.Read(ctx, paper.SourceKey, "abstract")
			if err != nil {
				return nil, err
			}
			doc.ID = ""
			doc.PaperID = paper.ID
			doc.ArtifactID = ""
			doc.ContentKind = "abstract"
			return deps.Evidence.SaveDocument(ctx, deps.Scope, doc)
		case "fulltext":
			return deps.Documents.Fetch(ctx, deps.Scope, paper.ID, args.Refresh)
		case "contents", "block":
			if args.DocumentID == "" {
				return nil, fmt.Errorf("%w：此阅读范围需要 document_id", domain.ErrInvalid)
			}
			doc, err := deps.Documents.Contents(ctx, deps.Scope, args.DocumentID)
			if err != nil {
				return nil, err
			}
			if doc.PaperID != paper.ID {
				return nil, domain.ErrInvalid
			}
			if args.Section == "contents" {
				return doc, nil
			}
			if args.BlockID == "" {
				return nil, fmt.Errorf("%w：按块阅读需要 block_id", domain.ErrInvalid)
			}
			return deps.Documents.Read(ctx, deps.Scope, doc.ID, args.BlockID)
		default:
			return nil, fmt.Errorf("%w：section 须为 abstract/fulltext/contents/block", domain.ErrInvalid)
		}
	})
}
func newDocumentEvidenceTools(deps AcademicToolsDeps) []tool.Tool {
	return []tool.Tool{
		newAcademicTool[EvidenceSearchArgs, []domain.DocumentHit]("search_project_evidence", "检索本项目已归档论文原文，返回可定位 DocumentID、BlockID 和有界原文；命中只是资料，尚未登记为证据。支持英文词项及中文短语，不搜索其他项目。", func(ctx context.Context, args EvidenceSearchArgs) ([]domain.DocumentHit, error) {
			return deps.Documents.Search(ctx, deps.Scope, args.Query, args.Limit)
		}, tool.ReadOnly()),
		newAcademicTool[SaveEvidenceArgs, EvidenceToolResult]("save_evidence", "将已读原文保存为项目证据。提供文档/块 ID 和唯一逐字 Quote，应用计算并校验准确区间；重复引用需消歧。返回 EvidenceID 和 citation_url，可在 Markdown 答复中引用。不编造页码或原文。", func(ctx context.Context, args SaveEvidenceArgs) (EvidenceToolResult, error) {
			e, err := deps.Documents.SaveQuote(ctx, deps.Scope, domain.Evidence{DocumentID: args.DocumentID, BlockID: args.BlockID, Quote: args.Quote, Field: args.Field, Value: args.Value})
			return EvidenceToolResult{Evidence: e, CitationURL: fmt.Sprintf("/p/%s/papers?paper=%s&evidence=%s", deps.Scope.ProjectID, e.PaperID, e.ID)}, err
		}),
	}
}
