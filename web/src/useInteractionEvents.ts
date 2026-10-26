import { useEffect } from "react";
import { api } from "./api/client";
import type { UITarget } from "./types";

const eventNames = ["research.progress", "research.report", "workspace.tool", "academic.tool", "assistant.delta", "runtime.interrupt", "turn.completed", "turn.accepted", "turn.restarted", "checkpoint_recovery_failed"];

// SSE 提醒页面同步持久快照，断线由 EventSource 按事件 ID 重连；轮询保留作补偿。
export function useInteractionEvents(projectID: string, target: UITarget, state: string | undefined, onEvents: () => Promise<void>, cursor = 0) {
  const active = ["queued", "running", "awaiting_input", "recovery_required"].includes(state || "");
  useEffect(() => {
    if (!active) return;
    const source = new EventSource(api.interactionEventsURL(projectID, { kind: target.kind, id: target.id }, cursor));
    let timer = 0;
    const onEvent = () => {
      if (!timer) timer = window.setTimeout(() => { timer = 0; void onEvents(); }, 80);
    };
    for (const name of eventNames) source.addEventListener(name, onEvent);
    source.onmessage = onEvent;
    return () => {
      window.clearTimeout(timer);
      source.close();
    };
    // 游标只在建立连接时读取；后续由浏览器 Last-Event-ID 跟踪，快照更新不重建连接。
  }, [projectID, target.kind, target.id, active, onEvents]);
}
