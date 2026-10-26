package ui

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/domain"
)

func TestPresentationNormalizesInteractions(t *testing.T) {
	for _, tc := range []struct {
		name, payload, kind string
		respond             bool
	}{
		{"澄清", `{"kind":"clarification","question":"选择范围","choices":[" 单卡 ",{"label":"多卡"}],"allow_custom":true}`, "clarification", true},
		{"旧字符串载荷", `"{\"question\":\"旧问题\",\"choices\":[\"甲\",\"乙\"]}"`, "clarification", true},
		{"计划", `{"kind":"research_plan","question":"审核计划","run_id":"run-1","revision":2,"hash":"plan-hash","plan":{"summary":"基线","queries":["forecast"]}}`, "research_plan", true},
		{"候选", `{"kind":"research_candidates","question":"选择论文","run_id":"run-1","revision":3,"hash":"candidate-hash","candidate_ids":["candidate-1"],"analyze_limit":2}`, "research_candidates", true},
		{"暂停", `{"kind":"research_pause","question":"已暂停","run_id":"run-1"}`, "research_pause", false},
		{"未知类型", `{"kind":"future_approval","question":"不应当作自由文本"}`, "unsupported", false},
		{"审核缺少版本", `{"kind":"research_plan","question":"审核","run_id":"run-1"}`, "unsupported", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := domain.TurnSnapshot{Turn: domain.Turn{ID: "turn-1", State: domain.TurnAwaitingInput, PendingQuestions: []byte(`[{"id":"interrupt-1","payload":` + tc.payload + `}]`)}, Events: []domain.SessionEvent{{ID: 9}, {ID: 4}}}
			view := Present(Target{Kind: "session", ID: "session-1"}, snapshot, nil)
			if view.UI.Version != "1" || view.UI.Interaction == nil || view.UI.Interaction.Kind != tc.kind || view.UI.Interaction.ID != "interrupt-1" || view.UI.EventCursor != 9 {
				t.Fatalf("交互投影错误: %+v", view.UI)
			}
			if slices.Contains(view.UI.AvailableActions, "respond") != tc.respond {
				t.Fatalf("错误开放回答动作: %+v", view.UI.AvailableActions)
			}
			if tc.name == "澄清" && (!slices.Equal(view.UI.Interaction.Choices, []string{"单卡", "多卡"}) || !view.UI.Interaction.AllowCustom) {
				t.Fatalf("选项或自定义输入丢失: %+v", view.UI.Interaction)
			}
			if _, err := json.Marshal(view); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPresentationDoesNotReviveOldOrUnreadableQuestions(t *testing.T) {
	snapshot := domain.TurnSnapshot{Turn: domain.Turn{State: domain.TurnCompleted, PendingQuestions: []byte(`[{"id":"old","payload":{"question":"旧问题"}}]`)}}
	if view := Present(Target{Kind: "session", ID: "s"}, snapshot, nil); view.UI.Interaction != nil || slices.Contains(view.UI.AvailableActions, "respond") {
		t.Fatalf("已结束轮次仍显示问题: %+v", view.UI)
	}
	snapshot.Turn.State = domain.TurnAwaitingInput
	for _, raw := range []string{`[{"id":"a","payload":null}]`, `[{"id":"a","payload":{}}]`} {
		snapshot.Turn.PendingQuestions = []byte(raw)
		if view := Present(Target{Kind: "session", ID: "s"}, snapshot, nil); view.UI.Interaction != nil || view.UI.InteractionError == "" {
			t.Fatalf("空载荷不应伪造普通问题: %+v", view.UI)
		}
	}
	snapshot.Turn.PendingQuestions = []byte(`broken`)
	if view := Present(Target{Kind: "session", ID: "s"}, snapshot, nil); view.UI.Interaction != nil || view.UI.InteractionError == "" {
		t.Fatalf("损坏数据应提示且不可回答: %+v", view.UI)
	}
	snapshot.Turn.PendingQuestions = []byte(`[{"id":"a","payload":{"question":"甲"}},{"id":"b","payload":{"question":"乙"}}]`)
	if view := Present(Target{Kind: "session", ID: "s"}, snapshot, nil); view.UI.Interaction != nil || view.UI.InteractionError == "" {
		t.Fatalf("不应静默丢弃多重中断: %+v", view.UI)
	}
}
