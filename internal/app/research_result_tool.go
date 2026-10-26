package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/goharness/goharness/pkg/tool"
)

// 每个图版本只开放实际实现的成果类型，不广告尚未交付的计划或报告。
type researchResultArgs struct {
	Kind      string                 `json:"kind" jsonschema:"当前仅支持 direction：保存待用户确认的研究方向提案"`
	Direction *domain.DirectionDraft `json:"direction" jsonschema:"方向提案；不能传入批准状态、项目身份或服务器路径"`
}
type ResearchResult struct {
	Kind         string            `json:"kind"`
	ID           string            `json:"id"`
	Revision     int64             `json:"revision"`
	ResourcePath string            `json:"resource_path"`
	Direction    *domain.Direction `json:"direction,omitempty"`
}

func newResearchResultTool(deps ResearchGraphDeps) tool.Tool {
	return newAcademicTool[researchResultArgs, ResearchResult]("submit_research_result", "保存研究成果。当前支持 kind=direction，提交研究方向提案、代表论文、关键问题、待验证空白及可行性；只有用户要求保存/整理方向时调用。结果处于 proposed，正式接受由用户在方向页操作。已有资料通过 read_file 的 research:// 地址读取，不新增读取工具。", func(ctx context.Context, args researchResultArgs) (ResearchResult, error) {
		if args.Kind != "direction" || args.Direction == nil {
			return ResearchResult{}, fmt.Errorf("%w：此版本仅支持 kind=direction 并要求 direction 内容", domain.ErrInvalid)
		}
		if deps.Directions == nil {
			return ResearchResult{}, domain.ErrUnavailable
		}
		data, err := json.Marshal(args)
		if err != nil {
			return ResearchResult{}, err
		}
		key := "turn:" + deps.TurnID + ":direction:" + sha256Hex(data)
		item, err := deps.Directions.Propose(ctx, deps.Scope, *args.Direction, deps.SessionID, deps.TurnID, key)
		if err != nil {
			return ResearchResult{}, err
		}
		return ResearchResult{Kind: "direction", ID: item.ID, Revision: item.Revision, ResourcePath: "research://directions/" + item.ID + ".json", Direction: &item}, nil
	})
}
