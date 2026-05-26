package app

import (
	"context"
	"fmt"
	"strings"

	"cyberstrike-ai/internal/agent"
	"cyberstrike-ai/internal/config"
	"cyberstrike-ai/internal/database"
	"cyberstrike-ai/internal/mcp"
	"cyberstrike-ai/internal/mcp/builtin"

	"go.uber.org/zap"
)

func projectIDFromConversation(db *database.DB, ctx context.Context) (string, error) {
	convID := agent.ConversationIDFromContext(ctx)
	if convID == "" {
		return "", fmt.Errorf("无法确定当前对话，请在对话上下文中使用项目事实工具")
	}
	pid, err := db.GetConversationProjectID(convID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(pid) == "" {
		return "", fmt.Errorf("当前对话未绑定项目，请先在对话中选择项目或创建带项目的对话")
	}
	return pid, nil
}

func textResult(msg string, isErr bool) *mcp.ToolResult {
	return &mcp.ToolResult{
		Content: []mcp.Content{{Type: "text", Text: msg}},
		IsError: isErr,
	}
}

// registerProjectFactTools 注册项目黑板 MCP 工具。
func registerProjectFactTools(mcpServer *mcp.Server, db *database.DB, cfg *config.Config, logger *zap.Logger) {
	if db == nil || cfg == nil || !cfg.Project.Enabled {
		if logger != nil {
			logger.Info("项目黑板工具未注册（未启用）")
		}
		return
	}

	upsertTool := mcp.Tool{
		Name:             builtin.ToolUpsertProjectFact,
		Description:      "写入或更新项目黑板事实。用于记录环境认知、目标信息、认证特征等（非正式漏洞条目）。同 fact_key 会覆盖更新。需要当前对话已绑定项目。",
		ShortDescription: "写入/更新项目事实",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"fact_key": map[string]interface{}{
					"type":        "string",
					"description": "项目内唯一 key，建议格式 category/slug，如 target/primary_domain",
				},
				"category": map[string]interface{}{
					"type":        "string",
					"description": "分类：target、auth、infra、business、note 等",
				},
				"summary": map[string]interface{}{
					"type":        "string",
					"description": "单行摘要（会注入到后续对话索引）",
				},
				"body": map[string]interface{}{
					"type":        "string",
					"description": "完整详情（POC、长文本等，仅 get_project_fact 返回）",
				},
				"confidence": map[string]interface{}{
					"type":        "string",
					"description": "confirmed | tentative | deprecated",
					"enum":        []string{"confirmed", "tentative", "deprecated"},
				},
				"pinned": map[string]interface{}{
					"type":        "boolean",
					"description": "是否优先出现在黑板索引",
				},
				"related_vulnerability_id": map[string]interface{}{
					"type":        "string",
					"description": "可选：关联的漏洞记录 ID",
				},
			},
			"required": []string{"fact_key", "summary"},
		},
	}

	mcpServer.RegisterTool(upsertTool, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		projectID, err := projectIDFromConversation(db, ctx)
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		factKey, _ := args["fact_key"].(string)
		summary, _ := args["summary"].(string)
		if strings.TrimSpace(factKey) == "" || strings.TrimSpace(summary) == "" {
			return textResult("错误: fact_key 与 summary 必填", true), nil
		}
		if len([]rune(summary)) > cfg.Project.FactSummaryMaxRunesEffective() {
			return textResult(fmt.Sprintf("错误: summary 过长（最多 %d 字）", cfg.Project.FactSummaryMaxRunesEffective()), true), nil
		}
		f := &database.ProjectFact{
			ProjectID:              projectID,
			FactKey:                factKey,
			Category:               strArg(args, "category"),
			Summary:                summary,
			Body:                   strArg(args, "body"),
			Confidence:             strArg(args, "confidence"),
			Pinned:                 boolArg(args, "pinned"),
			RelatedVulnerabilityID: strArg(args, "related_vulnerability_id"),
		}
		if convID := agent.ConversationIDFromContext(ctx); convID != "" {
			f.SourceConversationID = convID
		}
		created, err := db.UpsertProjectFact(f)
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		return textResult(fmt.Sprintf("事实已保存。\nfact_key: %s\nid: %s\nconfidence: %s", created.FactKey, created.ID, created.Confidence), false), nil
	})

	getTool := mcp.Tool{
		Name:             builtin.ToolGetProjectFact,
		Description:      "按 fact_key 获取项目事实完整 body 与元数据。摘要不足时必须调用本工具，禁止臆造细节。",
		ShortDescription: "按 key 获取事实详情",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"fact_key": map[string]interface{}{"type": "string", "description": "事实 key"},
			},
			"required": []string{"fact_key"},
		},
	}
	mcpServer.RegisterTool(getTool, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		projectID, err := projectIDFromConversation(db, ctx)
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		key := strings.TrimSpace(strArg(args, "fact_key"))
		if key == "" {
			return textResult("错误: fact_key 必填", true), nil
		}
		f, err := db.GetProjectFactByKey(projectID, key)
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		msg := fmt.Sprintf("fact_key: %s\ncategory: %s\nconfidence: %s\nsummary: %s\nupdated_at: %s\n\n--- body ---\n%s",
			f.FactKey, f.Category, f.Confidence, f.Summary, f.UpdatedAt.Format("2006-01-02 15:04:05"), f.Body)
		return textResult(msg, false), nil
	})

	listTool := mcp.Tool{
		Name:             builtin.ToolListProjectFacts,
		Description:      "列出当前项目的事实（分页）。",
		ShortDescription: "列出项目事实",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"category":   map[string]interface{}{"type": "string"},
				"confidence": map[string]interface{}{"type": "string"},
				"limit":      map[string]interface{}{"type": "integer"},
				"offset":     map[string]interface{}{"type": "integer"},
			},
		},
	}
	mcpServer.RegisterTool(listTool, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		projectID, err := projectIDFromConversation(db, ctx)
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		limit := intArg(args, "limit", 50)
		offset := intArg(args, "offset", 0)
		filter := database.ProjectFactListFilter{
			Category:   strArg(args, "category"),
			Confidence: strArg(args, "confidence"),
		}
		list, err := db.ListProjectFacts(projectID, filter, limit, offset)
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("共 %d 条（limit=%d offset=%d）:\n", len(list), limit, offset))
		for _, f := range list {
			b.WriteString(fmt.Sprintf("- [%s] %s — %s (%s)\n", f.FactKey, f.Category, f.Summary, f.Confidence))
		}
		return textResult(b.String(), false), nil
	})

	searchTool := mcp.Tool{
		Name:             builtin.ToolSearchProjectFacts,
		Description:      "按关键词搜索项目事实（summary/body/fact_key）。",
		ShortDescription: "搜索项目事实",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query":  map[string]interface{}{"type": "string"},
				"limit":  map[string]interface{}{"type": "integer"},
				"offset": map[string]interface{}{"type": "integer"},
			},
			"required": []string{"query"},
		},
	}
	mcpServer.RegisterTool(searchTool, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		projectID, err := projectIDFromConversation(db, ctx)
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		q := strings.TrimSpace(strArg(args, "query"))
		if q == "" {
			return textResult("错误: query 必填", true), nil
		}
		list, err := db.ListProjectFacts(projectID, database.ProjectFactListFilter{Search: q}, intArg(args, "limit", 30), intArg(args, "offset", 0))
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("搜索 \"%s\" 命中 %d 条:\n", q, len(list)))
		for _, f := range list {
			b.WriteString(fmt.Sprintf("- [%s] %s — %s\n", f.FactKey, f.Category, f.Summary))
		}
		return textResult(b.String(), false), nil
	})

	deprecateTool := mcp.Tool{
		Name:             builtin.ToolDeprecateProjectFact,
		Description:      "将事实标记为 deprecated，从黑板索引中排除。",
		ShortDescription: "废弃项目事实",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"fact_key": map[string]interface{}{"type": "string"},
			},
			"required": []string{"fact_key"},
		},
	}
	mcpServer.RegisterTool(deprecateTool, func(ctx context.Context, args map[string]interface{}) (*mcp.ToolResult, error) {
		projectID, err := projectIDFromConversation(db, ctx)
		if err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		key := strings.TrimSpace(strArg(args, "fact_key"))
		if err := db.DeprecateProjectFact(projectID, key); err != nil {
			return textResult("错误: "+err.Error(), true), nil
		}
		return textResult("事实已标记为 deprecated: "+key, false), nil
	})

	if logger != nil {
		logger.Info("项目黑板 MCP 工具注册成功")
	}
}

func strArg(args map[string]interface{}, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func boolArg(args map[string]interface{}, key string) bool {
	if v, ok := args[key].(bool); ok {
		return v
	}
	return false
}

func intArg(args map[string]interface{}, key string, def int) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	default:
		return def
	}
}
