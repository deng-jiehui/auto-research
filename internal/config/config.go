package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Config 只保存密钥引用，密钥值由启动入口读取。
type Config struct {
	Academic           AcademicConfig `json:"academic"`
	Listen             string         `json:"listen"`
	DataDir            string         `json:"data_dir"`
	ModelMode          string         `json:"model_mode"`
	AgentMode          string         `json:"agent_mode"`
	ModelEndpoint      string         `json:"model_endpoint"`
	ModelName          string         `json:"model_name"`
	APIKeyEnv          string         `json:"api_key_env"`
	AuthTokenEnv       string         `json:"auth_token_env"`
	WorkspaceRoot      string         `json:"workspace_root"`
	WorkspaceShell     string         `json:"workspace_shell"`
	DocumentPDFCommand string         `json:"document_pdf_command,omitempty"`
}

// AcademicConfig 独立于模型配置；真实来源失败时不得退回演示数据。
type AcademicConfig struct {
	Mode           string `json:"mode"`
	Command        string `json:"command,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

func (c AcademicConfig) Effective() AcademicConfig {
	if c.Mode == "" {
		c.Mode = "fixture"
	}
	if c.Command == "" {
		c.Command = "uvx"
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 60
	}
	return c
}

func Load(path string) (Config, error) {
	cfg := Config{Listen: "127.0.0.1:8080", DataDir: "./data", ModelMode: "fixture", AgentMode: "single", WorkspaceRoot: "."}
	f, err := os.Open(path)
	if err != nil {
		return cfg, fmt.Errorf("读取配置: %w", err)
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("配置格式错误: %w", err)
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return cfg, errors.New("配置只能包含一个 JSON 对象")
	}
	if cfg.AgentMode == "" {
		cfg.AgentMode = "single"
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	academic := c.Academic.Effective()
	if academic.Mode != "fixture" && academic.Mode != "arxiv_mcp" {
		return errors.New("学术来源模式必须为 fixture 或 arxiv_mcp")
	}
	if academic.TimeoutSeconds < 5 || academic.TimeoutSeconds > 120 {
		return errors.New("学术来源超时必须在 5 到 120 秒之间")
	}
	if strings.TrimSpace(c.DataDir) == "" {
		return errors.New("数据目录不能为空")
	}
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return errors.New("监听地址必须包含主机与端口")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 0 || portNumber > 65535 {
		return errors.New("监听端口无效")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		if c.AuthTokenEnv == "" || strings.TrimSpace(os.Getenv(c.AuthTokenEnv)) == "" {
			return errors.New("非回环监听必须配置非空认证环境变量")
		}
	}
	if c.AuthTokenEnv != "" && strings.TrimSpace(os.Getenv(c.AuthTokenEnv)) == "" {
		return errors.New("认证环境变量为空")
	}
	switch c.ModelMode {
	case "fixture":
	case "remote":
		endpoint, err := url.Parse(c.ModelEndpoint)
		if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || strings.TrimSpace(c.ModelName) == "" {
			return errors.New("远程模型必须配置有效 HTTP 端点与模型名称")
		}
	default:
		return errors.New("模型模式必须为 fixture 或 remote")
	}
	if c.AgentMode != "" && c.AgentMode != "single" && c.AgentMode != "multi" {
		return errors.New("Agent 模式必须为 single 或 multi")
	}
	return nil
}
