package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cyberstrike-ai/internal/config"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestUpdateConfigHitlAuditModelSavesAndPreservesHitlSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	raw := strings.Join([]string{
		"openai:",
		"  provider: openai",
		"  base_url: https://api.deepseek.com/v1",
		"  api_key: main-secret",
		"  model: deepseek-chat",
		"hitl:",
		"  audit_backend: openai",
		"  default_mode: approval",
		"  default_reviewer: audit_agent",
		"  tool_whitelist: [nmap, curl]",
		"  audit_agent_prompt: custom-approval-prompt",
		"  audit_model:",
		"    provider: openai_compatible",
		"    base_url: https://old-audit.example/v1",
		"    api_key: saved-audit-secret",
		"    model: old-audit-model",
		"    headers:",
		"      X-Audit-Gateway: fixture-header",
		"    reasoning:",
		"      profile: deepseek_compat",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	h := &ConfigHandler{configPath: path, config: cfg, logger: zap.NewNop()}
	router := gin.New()
	router.PUT("/config", h.UpdateConfig)

	update := func(body string, wantStatus int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != wantStatus {
			t.Fatalf("status = %d, want %d: %s", res.Code, wantStatus, res.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &out)
		return out
	}

	// Update model and URL while keeping the masked audit key.
	update(`{"hitl_audit_model":{"base_url":"https://api.deepseek.com/v1","api_key":"********","model":"deepseek-flash"}}`, http.StatusOK)

	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Hitl.AuditModel.Model != "deepseek-flash" || saved.Hitl.AuditModel.BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("audit model not updated: %+v", saved.Hitl.AuditModel)
	}
	if saved.Hitl.AuditModel.APIKey != "saved-audit-secret" {
		t.Fatalf("masked audit key was not preserved: %q", saved.Hitl.AuditModel.APIKey)
	}
	if saved.Hitl.DefaultMode != "approval" || saved.Hitl.DefaultReviewer != "audit_agent" ||
		len(saved.Hitl.ToolWhitelist) != 2 || saved.Hitl.AuditAgentPrompt != "custom-approval-prompt" ||
		saved.Hitl.AuditModel.Headers["X-Audit-Gateway"] != "fixture-header" ||
		saved.Hitl.AuditModel.Reasoning.Profile != "deepseek_compat" {
		t.Fatalf("updating audit model overwrote unrelated hitl settings: %+v", saved.Hitl)
	}

	// Clearing api_key clears the saved audit key so chat mode inherits main model key.
	update(`{"hitl_audit_model":{"provider":"","base_url":"","api_key":"","model":""}}`, http.StatusOK)
	saved, err = config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Hitl.AuditModel.APIKey != "" || saved.Hitl.AuditModel.Provider != "" || saved.Hitl.AuditModel.Model != "" {
		t.Fatalf("expected cleared audit model fields, got %+v", saved.Hitl.AuditModel)
	}

	// Validation: TypeSafe requires an explicit key, invalid URL/backend/provider and hitl conflict fail with 400.
	update(`{"hitl_audit_model":{"audit_backend":"typesafe"}}`, http.StatusBadRequest)
	update(`{"hitl_audit_model":{"base_url":"not-a-url"}}`, http.StatusBadRequest)
	update(`{"hitl_audit_model":{"audit_backend":"unknown"}}`, http.StatusBadRequest)
	update(`{"hitl_audit_model":{"provider":"unknown"}}`, http.StatusBadRequest)
	update(`{"hitl":{},"hitl_audit_model":{"model":"x"}}`, http.StatusBadRequest)

	// Valid TypeSafe update persists.
	update(`{"hitl_audit_model":{"audit_backend":"typesafe","api_key":"ts-secret","model":"jev-latest"}}`, http.StatusOK)
	saved, err = config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Hitl.AuditBackend != "typesafe" || saved.Hitl.AuditModel.APIKey != "ts-secret" {
		t.Fatalf("typesafe update did not persist: %+v", saved.Hitl)
	}

	// Rollback on save failure leaves in-memory config unchanged.
	h.configPath = filepath.Join(t.TempDir(), "missing-dir", "missing.yaml")
	update(`{"hitl_audit_model":{"model":"unsaved-model"}}`, http.StatusInternalServerError)
	if cfg.Hitl.AuditModel.Model != "jev-latest" {
		t.Fatalf("failed save mutated in-memory audit model: %q", cfg.Hitl.AuditModel.Model)
	}
}
