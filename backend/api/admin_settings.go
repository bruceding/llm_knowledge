package api

import (
	"context"
	"llm-knowledge/agent"
	"llm-knowledge/db"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

const (
	defaultTranslationApiBase = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	defaultTranslationModel   = "deepseek-v4-flash"

	// apiKeyMask is the placeholder returned in GET responses to mask the real key
	apiKeyMask = "••••••••"

	// llmBackendClaude / llmBackendPi 是 LLMBackend 仅有的两个合法取值。
	// 与 agent.BackendClaude / BackendPi 逐字对应,但此处用字面量而不是引用
	// agent 的常量:这两个值同时是 **API 契约**(前端下拉框的 option value),
	// 写死在 handler 旁边比跟着后端枚举漂移更安全。
	llmBackendClaude = "claude"
	llmBackendPi     = "pi"

	// piProbeTimeout 给切换前的 pi 可用性探测封顶。
	// agent.ProbePi 内部的 `pi --version` 自带 5s 超时,这里再包一层是为了防止
	// 它将来变慢时把整个 PUT 请求拖死(另两段校验是纯本地计算,不花时间)。
	piProbeTimeout = 15 * time.Second
)

// maskApiKey returns a masked version of the API key for display.
// Shows last 4 chars if key is long enough, otherwise returns the mask placeholder.
func maskApiKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) > 4 {
		return "****" + key[len(key)-4:]
	}
	return apiKeyMask
}

// ensureGlobalSettings loads the singleton GlobalSettings row.
// Uses empty-condition FirstOrCreate to avoid the GORM bug where
// non-zero search conditions create duplicate rows when values differ from defaults.
func ensureGlobalSettings() (db.GlobalSettings, error) {
	var settings db.GlobalSettings
	result := db.DB.FirstOrCreate(&settings)
	if result.Error != nil {
		return settings, result.Error
	}
	if settings.ID == 0 {
		// Row was just created with zero values; set defaults
		settings.TranslationApiBase = defaultTranslationApiBase
		settings.TranslationModel = defaultTranslationModel
		db.DB.Save(&settings)
	}
	return settings, nil
}

type AdminSettingsHandler struct{}

// globalSettingsResponse builds the JSON response for global settings,
// including a masked API key instead of the real one.
func globalSettingsResponse(settings db.GlobalSettings) echo.Map {
	return echo.Map{
		"id":                 settings.ID,
		"translationEnabled": settings.TranslationEnabled,
		"translationApiBase": settings.TranslationApiBase,
		"translationApiKey":  maskApiKey(settings.TranslationApiKey),
		"translationModel":   settings.TranslationModel,
		"llmBackend":         settings.LLMBackend,
		"createdAt":          settings.CreatedAt,
		"updatedAt":          settings.UpdatedAt,
	}
}

// GetGlobalSettings returns global settings (admin only)
// GET /api/admin/settings
func (h *AdminSettingsHandler) GetGlobalSettings(c echo.Context) error {
	settings, err := ensureGlobalSettings()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get global settings"})
	}
	return c.JSON(http.StatusOK, globalSettingsResponse(settings))
}

// UpdateGlobalSettings updates global settings (admin only)
// PUT /api/admin/settings
func (h *AdminSettingsHandler) UpdateGlobalSettings(c echo.Context) error {
	settings, err := ensureGlobalSettings()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to get global settings"})
	}

	var input struct {
		TranslationEnabled *bool  `json:"translationEnabled"`
		TranslationApiBase string `json:"translationApiBase"`
		TranslationApiKey  string `json:"translationApiKey"`
		TranslationModel   string `json:"translationModel"`
		// LLMBackend 沿用本 handler 既有的部分更新风格:**空串表示不改**。
		LLMBackend string `json:"llmBackend"`
	}
	if err := c.Bind(&input); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "invalid input"})
	}

	// Apply partial updates
	if input.TranslationEnabled != nil {
		settings.TranslationEnabled = *input.TranslationEnabled
	}
	if input.TranslationApiBase != "" {
		settings.TranslationApiBase = input.TranslationApiBase
	}
	if input.TranslationModel != "" {
		settings.TranslationModel = input.TranslationModel
	}
	// Only update API key if a real value is provided (not the mask placeholder)
	if input.TranslationApiKey != "" && input.TranslationApiKey != apiKeyMask && !strings.HasPrefix(input.TranslationApiKey, "****") {
		settings.TranslationApiKey = input.TranslationApiKey
	}

	// 后端开关。切到 pi 之前先探测,不可用就 400 —— 否则管理员会把一个起不来的
	// 后端设成全局生效,而症状是「所有聊天请求 500」,回溯到「刚刚改过设置」并不直观。
	backendChanged := false
	if input.LLMBackend != "" {
		switch input.LLMBackend {
		case llmBackendClaude:
			// claude 不探测:它是默认后端与回退值,且 ClaudeProtocol.Probe 本就是
			// no-op(保持既有行为)。给它加探测会让「后端已经坏了的时候切回 claude
			// 自救」这条路也被堵住。
			settings.LLMBackend = llmBackendClaude
			backendChanged = true
		case llmBackendPi:
			ctx, cancel := context.WithTimeout(c.Request().Context(), piProbeTimeout)
			err := agent.ProbePi(ctx)
			cancel()
			if err != nil {
				return c.JSON(http.StatusBadRequest, echo.Map{
					"error": "无法切换到 pi 后端:" + err.Error(),
				})
			}
			settings.LLMBackend = llmBackendPi
			backendChanged = true
		default:
			// 400 而不是静默忽略:前端下拉框只有两个选项,能走到这里说明请求是
			// 手造的或前后端版本不一致,静默忽略会让调用方以为切换成功了。
			// 消息与中间件的 403 "admin access required" 必须可区分(规格要求):
			// 403 = 没权限,400 = 有权限但取值非法。
			return c.JSON(http.StatusBadRequest, echo.Map{
				"error": "llmBackend must be \"claude\" or \"pi\", got \"" + input.LLMBackend + "\"",
			})
		}
	}

	// Validate when translation is enabled
	if settings.TranslationEnabled {
		if settings.TranslationApiKey == "" {
			return c.JSON(http.StatusBadRequest, echo.Map{"error": "API key required when translation is enabled"})
		}
		if settings.TranslationApiBase == "" {
			return c.JSON(http.StatusBadRequest, echo.Map{"error": "API base URL required when translation is enabled"})
		}
		if settings.TranslationModel == "" {
			return c.JSON(http.StatusBadRequest, echo.Map{"error": "Model name required when translation is enabled"})
		}
	}

	if err := db.DB.Save(&settings).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": "failed to save global settings"})
	}

	// 保存成功后主动失效 resolver 缓存,否则新后端要等 TTL(5s)过期才生效。
	// 只在真的改了后端时调:改翻译配置不该把会话层的 Protocol 缓存也丢掉。
	if backendChanged {
		agent.Invalidate()
	}

	return c.JSON(http.StatusOK, globalSettingsResponse(settings))
}
