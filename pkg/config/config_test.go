package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAgentConfigEffectiveBackend(t *testing.T) {
	tests := []struct {
		name string
		cfg  AgentConfig
		want string
	}{
		{name: "native default", cfg: AgentConfig{}, want: AgentBackendBuiltin},
		{name: "explicit native", cfg: AgentConfig{Backend: " BUILTIN "}, want: AgentBackendBuiltin},
		{name: "explicit native wins over legacy paths", cfg: AgentConfig{Backend: "builtin", Home: "~/.picoclaw"}, want: AgentBackendBuiltin},
		{name: "explicit legacy", cfg: AgentConfig{Backend: "picoclaw"}, want: AgentBackendPicoClaw},
		{name: "legacy home migration", cfg: AgentConfig{Home: "~/.picoclaw"}, want: AgentBackendPicoClaw},
		{name: "legacy config migration", cfg: AgentConfig{Config: "~/.picoclaw/config.json"}, want: AgentBackendPicoClaw},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.EffectiveBackend(); got != tt.want {
				t.Fatalf("EffectiveBackend() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no user home directory")
	}
	if got := ExpandHome("~/agents"); got != filepath.Join(home, "agents") {
		t.Fatalf("ExpandHome(~/agents) = %q, want %q", got, filepath.Join(home, "agents"))
	}
	if got := ExpandHome(" /tmp/x "); got != "/tmp/x" {
		t.Fatalf("ExpandHome passthrough = %q, want trimmed input", got)
	}
}

func TestDefaultConfigTemplateIsValidYAML(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal([]byte(defaultConfigTemplate()), &cfg); err != nil {
		t.Fatalf("default config template is invalid: %v", err)
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	t.Setenv(EnvServerPort, "9999")
	t.Setenv(EnvServerBindAddress, "0.0.0.0")
	t.Setenv(EnvServerAccessToken, "test123")

	cfg := DefaultConfig()
	// 模拟 config.yaml 里已有的值：env 优先级更高，必须覆盖
	cfg.Server.Port = 8106
	cfg.Server.BindAddress = "127.0.0.1"
	cfg.Server.AccessToken = "from-yaml"
	applyEnvOverrides(cfg)

	if cfg.Server.Port != 9999 {
		t.Errorf("Port = %d, want 9999 (env override)", cfg.Server.Port)
	}
	if cfg.Server.BindAddress != "0.0.0.0" {
		t.Errorf("BindAddress = %q, want %q (env override)", cfg.Server.BindAddress, "0.0.0.0")
	}
	if cfg.Server.AccessToken != "test123" {
		t.Errorf("AccessToken = %q, want %q (env beats yaml)", cfg.Server.AccessToken, "test123")
	}
}

func TestApplyEnvOverridesNoEnvKeepsConfig(t *testing.T) {
	// 确保没有相关环境变量时不改动配置（防止继承自 shell 的同名变量干扰断言）
	t.Setenv(EnvServerPort, "")
	t.Setenv(EnvServerBindAddress, "")
	t.Setenv(EnvServerAccessToken, "")

	cfg := DefaultConfig()
	cfg.Server.Port = 8200
	cfg.Server.BindAddress = "127.0.0.1"
	cfg.Server.AccessToken = "keep"
	applyEnvOverrides(cfg)

	if cfg.Server.Port != 8200 || cfg.Server.BindAddress != "127.0.0.1" || cfg.Server.AccessToken != "keep" {
		t.Fatalf("empty env must not override: got %+v", cfg.Server)
	}
}

func TestApplyEnvOverridesInvalidPortIgnored(t *testing.T) {
	t.Setenv(EnvServerPort, "not-a-number")

	cfg := DefaultConfig()
	cfg.Server.Port = 8106
	applyEnvOverrides(cfg)

	if cfg.Server.Port != 8106 {
		t.Errorf("invalid port must be ignored, got %d", cfg.Server.Port)
	}
}

func TestApplyEnvOverridesPartial(t *testing.T) {
	// 只设置 token 一项，其余字段不动 —— 对应 compose 只注入密钥的场景
	t.Setenv(EnvServerAccessToken, "only-token")

	cfg := DefaultConfig()
	applyEnvOverrides(cfg)

	if cfg.Server.AccessToken != "only-token" {
		t.Errorf("AccessToken = %q, want %q", cfg.Server.AccessToken, "only-token")
	}
	if cfg.Server.Port != 8106 || cfg.Server.BindAddress != "127.0.0.1" {
		t.Errorf("unrelated fields must keep defaults: got %+v", cfg.Server)
	}
}
