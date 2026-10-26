import { useCallback, useEffect, useState } from "react";
import type { AgentMode } from "../types";
import { api, errorText } from "../api/client";

export function useAgentMode(projectID: string, sessionID: string) {
  const [mode, setMode] = useState<AgentMode>("single");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const storageKey = `autoresearch.agent-mode.${projectID}.${sessionID}`;

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    void api.agentSettings().then((settings) => {
      if (!active) return;
      let preferred: string | null = null;
      try { preferred = window.localStorage.getItem(storageKey); } catch { /* 浏览器禁用存储时沿用服务默认值。 */ }
      setMode(preferred === "single" || preferred === "multi" ? preferred : settings.mode);
    }).catch((reason) => {
      if (active) setError(errorText(reason));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [storageKey, retry]);

  const choose = useCallback((value: AgentMode) => {
    setMode(value);
    try { window.localStorage.setItem(storageKey, value); } catch { /* 存储失败不妨碍当前会话切换。 */ }
  }, [storageKey]);

  return { mode, choose, loading, error, reload: () => setRetry((value) => value + 1) };
}

export function AgentModeSelector({ mode, onChange, disabled, error, onRetry }: {
  mode: AgentMode;
  onChange: (mode: AgentMode) => void;
  disabled: boolean;
  error: string;
  onRetry: () => void;
}) {
  return <div className="agent-mode-control">
    <div className="agent-mode-buttons" role="group" aria-label="Agent 模式">
      <button type="button" aria-pressed={mode === "single"} disabled={disabled || !!error} onClick={() => onChange("single")}>单 Agent</button>
      <button type="button" aria-pressed={mode === "multi"} disabled={disabled || !!error} onClick={() => onChange("multi")}>多 Agent</button>
    </div>
    <small>{mode === "multi" ? "主助手可委派独立子任务，下次发送生效" : "同一助手连续研究，下次发送生效"}</small>
    {error && <p className="form-error" role="alert">无法读取模式：{error} <button type="button" className="text-button" onClick={onRetry}>重试</button></p>}
  </div>;
}
