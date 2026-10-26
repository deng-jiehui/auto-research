package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/deng-jiehui/auto-research/internal/app"
	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/ui"
)

func (a *api) respondUI(w http.ResponseWriter, r *http.Request, target ui.Target) {
	var input app.ResumeInput
	if err := decode(w, r, &input); err != nil {
		a.fail(w, r, err)
		return
	}
	result, err := a.ui.Respond(r.Context(), scope(r), target, r.PathValue("turnID"), input)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

// uiEvents 只适配传输格式；持久游标和目标解析由共用交互服务提供。
func (a *api) uiEvents(w http.ResponseWriter, r *http.Request, target ui.Target) {
	after := int64(queryInt(r, "after", 0))
	sse := strings.Contains(r.Header.Get("Accept"), "text/event-stream")
	if sse {
		if parsed, err := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64); err == nil && parsed > after {
			after = parsed
		}
	}
	limit := queryInt(r, "limit", 100)
	events, err := a.ui.Events(r.Context(), scope(r), target, after, limit)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !sse {
		writeJSON(w, http.StatusOK, map[string]any{"items": events})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.fail(w, r, domain.ErrUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		for _, event := range events {
			data, err := json.Marshal(event)
			if err != nil {
				return
			}
			if _, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Kind, data); err != nil {
				return
			}
			after = event.ID
		}
		if len(events) > 0 {
			flusher.Flush()
		}
		snapshot, err := a.ui.Snapshot(r.Context(), scope(r), target, 1)
		if err != nil || !snapshot.Turn.Active() {
			// 终态同样完整回放分页事件，再关闭连接。
			if err == nil {
				events, err = a.ui.Events(r.Context(), scope(r), target, after, limit)
				if err == nil && len(events) > 0 {
					continue
				}
			}
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
		events, err = a.ui.Events(r.Context(), scope(r), target, after, limit)
		if err != nil {
			return
		}
	}
}
