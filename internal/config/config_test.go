package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/deng-jiehui/auto-research/internal/config"
)

func TestValidateRejectsIncompleteOrExposedConfiguration(t *testing.T) {
	tests := []struct {
		name string
		edit func(*config.Config)
	}{
		{"没有数据目录", func(c *config.Config) { c.DataDir = "" }},
		{"远程模型缺少端点", func(c *config.Config) { c.ModelMode = "remote"; c.ModelName = "chat" }},
		{"远程模型缺少名称", func(c *config.Config) { c.ModelMode = "remote"; c.ModelEndpoint = "https://example.com/v1" }},
		{"未知模型模式", func(c *config.Config) { c.ModelMode = "fallback" }},
		{"外网监听未设置认证", func(c *config.Config) { c.Listen = "0.0.0.0:8080" }},
		{"未解析主机不是回环保证", func(c *config.Config) { c.Listen = "research.example:8080" }},
		{"认证环境变量为空", func(c *config.Config) { c.Listen = ":8080"; c.AuthTokenEnv = "AUTORESEARCH_TEST_EMPTY_TOKEN" }},
		{"监听地址无端口", func(c *config.Config) { c.Listen = "127.0.0.1" }},
	}
	t.Setenv("AUTORESEARCH_TEST_EMPTY_TOKEN", "")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{Listen: "127.0.0.1:8080", DataDir: t.TempDir(), ModelMode: "fixture"}
			tt.edit(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("无效配置允许启动")
			}
		})
	}
}

func TestLoadDefaultsAndRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"data_dir":"./data"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:8080" || cfg.ModelMode != "fixture" {
		t.Fatalf("默认配置不正确: %+v", cfg)
	}
	if cfg.AgentMode != "single" {
		t.Fatalf("默认 Agent 模式=%q，期望 single", cfg.AgentMode)
	}
	if cfg.WorkspaceRoot != "." {
		t.Fatalf("默认工作区根目录不正确: %q", cfg.WorkspaceRoot)
	}
	if err := os.WriteFile(path, []byte(`{"data_dir":"./data","api_key":"secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err == nil {
		t.Fatal("直接保存密钥的未知字段未被拒绝")
	}
	t.Setenv("AUTORESEARCH_TEST_TOKEN", "test-token")
	cfg.Listen, cfg.AuthTokenEnv = "[::]:8080", "AUTORESEARCH_TEST_TOKEN"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAgentModes(t *testing.T) {
	for _, mode := range []string{"single", "multi"} {
		cfg := config.Config{Listen: "127.0.0.1:8080", DataDir: t.TempDir(), ModelMode: "fixture", AgentMode: mode}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("模式 %q 不应被拒绝: %v", mode, err)
		}
	}
	cfg := config.Config{Listen: "127.0.0.1:8080", DataDir: t.TempDir(), ModelMode: "fixture", AgentMode: "unknown"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("未知 Agent 模式未被拒绝")
	}
}

func TestLoadEmptyAgentModeUsesSingle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"agent_mode":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.AgentMode != "single" {
		t.Fatalf("空模式应解析成可启动的 single：mode=%q err=%v", cfg.AgentMode, err)
	}
}

func TestLoadAcademicConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for _, mode := range []string{"fixture", "arxiv_mcp"} {
		if err := os.WriteFile(path, []byte(`{"academic":{"mode":"`+mode+`","command":"uvx","timeout_seconds":60}}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load(path); err != nil {
			t.Fatalf("合法来源配置不能加载: %v", err)
		}
	}
	for _, raw := range []string{`{"academic":{"mode":"unknown"}}`, `{"academic":{"mode":"arxiv_mcp","timeout_seconds":-1}}`} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load(path); err == nil {
			t.Fatalf("无效配置通过: %s", raw)
		}
	}
}
