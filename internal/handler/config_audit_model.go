package handler

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"cyberstrike-ai/internal/config"
)

// HitlAuditModelUpdate edits hitl.audit_backend and hitl.audit_model without
// overwriting unrelated HITL settings such as tool_whitelist or prompts.
type HitlAuditModelUpdate struct {
	AuditBackend *string `json:"audit_backend,omitempty"`
	Provider     *string `json:"provider,omitempty"`
	BaseURL      *string `json:"base_url,omitempty"`
	APIKey       *string `json:"api_key,omitempty"`
	Model        *string `json:"model,omitempty"`
}

func cloneHitlConfig(src config.HitlConfig) config.HitlConfig {
	dst := src
	dst.ToolWhitelist = slices.Clone(src.ToolWhitelist)
	dst.AuditModel.Headers = maps.Clone(src.AuditModel.Headers)
	if src.AuditModel.Reasoning.ExtraRequestFields != nil {
		dst.AuditModel.Reasoning.ExtraRequestFields = maps.Clone(src.AuditModel.Reasoning.ExtraRequestFields)
	}
	if src.DefaultTimeoutSeconds != nil {
		v := *src.DefaultTimeoutSeconds
		dst.DefaultTimeoutSeconds = &v
	}
	if src.RetentionDays != nil {
		v := *src.RetentionDays
		dst.RetentionDays = &v
	}
	return dst
}

func (u HitlAuditModelUpdate) merge(cfg *config.Config) (*config.HitlConfig, error) {
	hitl := cloneHitlConfig(cfg.Hitl)

	if u.AuditBackend != nil {
		raw := strings.ToLower(strings.TrimSpace(*u.AuditBackend))
		switch raw {
		case "", config.HitlAuditBackendOpenAI:
			hitl.AuditBackend = config.HitlAuditBackendOpenAI
		case config.HitlAuditBackendTypeSafe, "jev", "type-safe", "typesafe-ai":
			hitl.AuditBackend = config.HitlAuditBackendTypeSafe
		default:
			return nil, fmt.Errorf("不支持的审计后端: %s", strings.TrimSpace(*u.AuditBackend))
		}
	}

	if u.Provider != nil {
		raw := strings.ToLower(strings.TrimSpace(*u.Provider))
		switch raw {
		case "":
			hitl.AuditModel.Provider = ""
		case "openai", "openai_compatible":
			hitl.AuditModel.Provider = "openai_compatible"
		case "claude", "anthropic":
			hitl.AuditModel.Provider = "claude"
		default:
			return nil, fmt.Errorf("不支持的审计模型提供商: %s", strings.TrimSpace(*u.Provider))
		}
	}

	if u.BaseURL != nil {
		raw := strings.TrimSpace(*u.BaseURL)
		if raw != "" {
			parsed, err := url.Parse(raw)
			if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
				return nil, fmt.Errorf("API 地址必须是包含 https:// 或 http:// 的完整 URL")
			}
		}
		hitl.AuditModel.BaseURL = raw
	}

	if u.APIKey != nil {
		hitl.AuditModel.APIKey = strings.TrimSpace(*u.APIKey)
	}

	if u.Model != nil {
		hitl.AuditModel.Model = strings.TrimSpace(*u.Model)
	}

	return &hitl, nil
}
