package api

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"tt-inspector/internal/engine"
	"tt-inspector/internal/notifier"
	"tt-inspector/internal/store"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type ErrorScanHandler struct {
	store         *store.ErrorScanStore
	logStore      *store.LogStore
	classifyStore *store.ErrorClassifyStore
	channelStore  *store.ChannelStore
	scanner       *engine.ErrorScanner
	logger        *zap.Logger
}

func (h *ErrorScanHandler) List(c *gin.Context) {
	rules, err := h.store.List()
	if err != nil {
		h.logger.Error("list error scan rules failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rules == nil {
		rules = []store.ErrorScanRule{}
	}
	c.JSON(http.StatusOK, gin.H{"data": rules})
}

func (h *ErrorScanHandler) Create(c *gin.Context) {
	var rule store.ErrorScanRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if rule.Code == "" || rule.Name == "" || rule.Category == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code, name, category are required"})
		return
	}

	if err := h.store.Create(&rule); err != nil {
		h.logger.Error("create error scan rule failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": rule})
}

func (h *ErrorScanHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	// 用 map 接收 JSON body，支持部分更新
	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.store.UpdateFields(id, updates); err != nil {
		h.logger.Error("update error scan rule failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	updated, _ := h.store.Get(id)
	c.JSON(http.StatusOK, gin.H{"data": updated})
}

func (h *ErrorScanHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if err := h.store.Delete(id); err != nil {
		h.logger.Error("delete error scan rule failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func (h *ErrorScanHandler) ToggleAlert(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	rule, err := h.store.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "rule not found"})
		return
	}

	if err := h.store.SetAlertEnabled(id, !rule.IsAlertEnabled); err != nil {
		h.logger.Error("toggle alert failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (h *ErrorScanHandler) ToggleSuppressed(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	rule, err := h.store.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "rule not found"})
		return
	}

	if err := h.store.SetSuppressed(id, !rule.Suppressed); err != nil {
		h.logger.Error("toggle suppressed failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (h *ErrorScanHandler) UpdateGranularity(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var body struct {
		ScanGranularity  string `json:"scan_granularity"`
		AlertGranularity string `json:"alert_granularity"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.store.UpdateGranularity(id, body.ScanGranularity, body.AlertGranularity); err != nil {
		h.logger.Error("update granularity failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (h *ErrorScanHandler) UpdateThresholdAndExcludes(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var body struct {
		AlertThreshold  int    `json:"alert_threshold"`
		ExcludeChannels string `json:"exclude_channels"`
		ExcludeUsers    string `json:"exclude_users"`
		FilterMode      string `json:"filter_mode"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.store.UpdateThresholdAndExcludes(id, body.AlertThreshold, body.ExcludeChannels, body.ExcludeUsers, body.FilterMode); err != nil {
		h.logger.Error("update threshold and excludes failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (h *ErrorScanHandler) GetPollInterval(c *gin.Context) {
	seconds := int(h.scanner.GetPollInterval().Seconds())
	c.JSON(http.StatusOK, gin.H{"data": seconds})
}

func (h *ErrorScanHandler) SetPollInterval(c *gin.Context) {
	var body struct {
		Seconds int `json:"seconds"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.Seconds < 10 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "poll interval must be at least 10 seconds"})
		return
	}
	h.scanner.SetPollInterval(body.Seconds)
	c.JSON(http.StatusOK, gin.H{"data": body.Seconds})
}

func (h *ErrorScanHandler) TestWebhook(c *gin.Context) {
	var body notifier.FeishuAppConfig
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.AppID == "" || body.AppSecret == "" || body.ChatID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_id, app_secret, chat_id are required"})
		return
	}

	err := h.scanner.TestFeishuApp(body)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "test card sent successfully"})
}

func (h *ErrorScanHandler) TriggerScan(c *gin.Context) {
	h.scanner.TriggerScan()
	c.JSON(http.StatusOK, gin.H{"message": "scan triggered"})
}

func (h *ErrorScanHandler) GetRecentErrorSamples(c *gin.Context) {
	samples, err := h.logStore.GetRecentErrorSamples(20)
	if err != nil {
		h.logger.Error("get recent error samples failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if samples == nil {
		samples = []store.ErrorSample{}
	}
	c.JSON(http.StatusOK, gin.H{"data": samples})
}

func (h *ErrorScanHandler) GetOverview(c *gin.Context) {
	rules, err := h.store.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 合并精细化规则和分类规则的关键词（与 GetUnmatchedSamples 保持一致）
	patternSet := make(map[string]bool)
	activeCount := int64(0)
	for _, r := range rules {
		if r.IsAlertEnabled && !r.Suppressed {
			activeCount++
		}
		if r.PatternKeyword != "" {
			patternSet[r.PatternKeyword] = true
		}
	}
	classifyRules, _ := h.classifyStore.List()
	for _, cr := range classifyRules {
		if cr.Keyword != "" {
			patternSet[cr.Keyword] = true
		}
	}
	patterns := make([]string, 0, len(patternSet))
	for p := range patternSet {
		patterns = append(patterns, p)
	}

	windowHours := 1
	if wh := c.Query("window"); wh != "" {
		if v, err := strconv.Atoi(wh); err == nil && v > 0 {
			windowHours = v
		}
	}

	stats, err := h.logStore.GetOverviewStats(patterns, windowHours)
	if err != nil {
		h.logger.Error("get overview stats failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	stats.ActiveRules = activeCount
	c.JSON(http.StatusOK, gin.H{"data": stats})
}

func (h *ErrorScanHandler) GetUnmatchedSamples(c *gin.Context) {
	// 合并精细化规则和分类规则的关键词，确保任一来源的规则都能排除已匹配日志
	patternSet := make(map[string]bool)
	rules, _ := h.store.List()
	for _, r := range rules {
		if r.PatternKeyword != "" {
			patternSet[r.PatternKeyword] = true
		}
	}
	classifyRules, _ := h.classifyStore.List()
	for _, cr := range classifyRules {
		if cr.Keyword != "" {
			patternSet[cr.Keyword] = true
		}
	}
	patterns := make([]string, 0, len(patternSet))
	for p := range patternSet {
		patterns = append(patterns, p)
	}

	// 使用与概览统计相同的时间窗口
	windowHours := 1
	if wh := c.Query("window"); wh != "" {
		if v, err := strconv.Atoi(wh); err == nil && v > 0 {
			windowHours = v
		}
	}

	samples, err := h.logStore.GetUnmatchedErrorSamples(patterns, 20, windowHours)
	if err != nil {
		h.logger.Error("get unmatched samples failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if samples == nil {
		samples = []store.ErrorSampleWithId{}
	}
	c.JSON(http.StatusOK, gin.H{"data": samples})
}

func (h *ErrorScanHandler) CreateFromSample(c *gin.Context) {
	var body struct {
		Content     string `json:"content"`
		Other       string `json:"other"`
		ModelName   string `json:"model_name"`
		ChannelName string `json:"channel_name"`
		Username    string `json:"username"`
		Code        string `json:"code"`
		Name        string `json:"name"`
		Category    string `json:"category"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 从 content 中提取关键词：去掉 status_code=xxx 前缀，取第一个有意义的部分
	keyword := extractKeyword(body.Content)

	rule := store.ErrorScanRule{
		Code:           body.Code,
		Name:           body.Name,
		Category:       body.Category,
		SubCategory:    body.Name,
		PatternKeyword: keyword,
		TopModel:       body.ModelName,
		TopChannel:     body.ChannelName,
		TopUser:        body.Username,
		AlertThreshold: 5,
		IsAlertEnabled: true,
		Severity:       "warning",
		FilterMode:     "exclude",
		RawLogs:        "[]",
	}
	if err := h.store.Create(&rule); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 同步创建/合并分类规则，确保 classifyLog 能匹配该关键词
	statusCode := extractStatusCode(body.Content)
	classifyRule := store.ErrorClassifyRule{
		Keyword:        keyword,
		StatusCode:     statusCode,
		Category:       body.Category,
		SubCategory:    body.Name,
		Recommendation: "",
		Priority:       50,
	}
	// 关键词唯一：已存在则更新合并
	existingRules, _ := h.classifyStore.List()
	merged := false
	for _, cr := range existingRules {
		if strings.EqualFold(cr.Keyword, keyword) {
			if cr.StatusCode != statusCode && statusCode != 0 {
				classifyRule.StatusCode = 0
			}
			classifyRule.Id = cr.Id
			if err := h.classifyStore.Update(&classifyRule); err != nil {
				h.logger.Warn("merge classify rule failed (non-fatal)", zap.Error(err))
			} else {
				merged = true
			}
			break
		}
	}
	if !merged {
		if err := h.classifyStore.Create(&classifyRule); err != nil {
			h.logger.Warn("create classify rule failed (non-fatal)", zap.Error(err))
		} else {
			h.logger.Info("auto-created classify rule alongside error scan rule",
				zap.String("keyword", keyword))
		}
	}

	c.JSON(http.StatusOK, gin.H{"data": rule})
}

// extractKeyword 从 content 中提取关键字：去掉 status_code=xxx 前缀，取剩余部分作为关键词
func extractKeyword(content string) string {
	// 去掉开头的 "status_code=xxx," 或 "status_code=xxx "
	content = strings.TrimSpace(content)
	if after, found := strings.CutPrefix(strings.ToLower(content), "status_code="); found {
		// 跳过数字和分隔符
		rest := after
		for len(rest) > 0 && (rest[0] >= '0' && rest[0] <= '9' || rest[0] == ',' || rest[0] == ' ') {
			rest = rest[1:]
		}
		rest = strings.TrimSpace(rest)
		// 去掉 "(request id:...)" 后缀
		if idx := strings.Index(rest, "(request id:"); idx >= 0 {
			rest = strings.TrimSpace(rest[:idx])
		}
		if rest != "" {
			if len(rest) > 120 {
				rest = rest[:120]
			}
			return rest
		}
	}
	// fallback: 取 content 前 120 字符
	if len(content) > 120 {
		return content[:120]
	}
	return content
}

// extractStatusCode 从 content 中提取状态码，如 "status_code=400" → 400
func extractStatusCode(content string) int {
	re := regexp.MustCompile(`status_code[=:]\s*(\d{3})`)
	matches := re.FindStringSubmatch(content)
	if len(matches) >= 2 {
		if v, err := strconv.Atoi(matches[1]); err == nil {
			return v
		}
	}
	return 0
}

func (h *ErrorScanHandler) GetFallbackConfig(c *gin.Context) {
	enabled, webhook := h.scanner.GetFallbackConfig()
	c.JSON(http.StatusOK, gin.H{"data": map[string]interface{}{
		"enabled": enabled,
		"webhook": webhook,
	}})
}

func (h *ErrorScanHandler) SetFallbackConfig(c *gin.Context) {
	var body struct {
		Enabled bool   `json:"enabled"`
		Webhook string `json:"webhook"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.scanner.SetFallbackConfig(body.Enabled, body.Webhook)
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (h *ErrorScanHandler) GetAlertAtUsers(c *gin.Context) {
	users := h.scanner.GetAlertAtUsers()
	c.JSON(http.StatusOK, gin.H{"data": users})
}

func (h *ErrorScanHandler) SetAlertAtUsers(c *gin.Context) {
	var body struct {
		Users []string `json:"users"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.scanner.SetAlertAtUsers(body.Users)
	c.JSON(http.StatusOK, gin.H{"data": body.Users})
}

func (h *ErrorScanHandler) GetFeishuAppConfig(c *gin.Context) {
	cfg := h.scanner.GetFeishuAppConfig()
	c.JSON(http.StatusOK, gin.H{"data": cfg})
}

func (h *ErrorScanHandler) SetFeishuAppConfig(c *gin.Context) {
	var body notifier.FeishuAppConfig
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.scanner.SetFeishuAppConfig(body)
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (h *ErrorScanHandler) GetGroupedStats(c *gin.Context) {
	dimension := c.DefaultQuery("dimension", "channel")
	windowHours := 1
	if wh := c.Query("window"); wh != "" {
		if v, err := strconv.Atoi(wh); err == nil && v > 0 {
			windowHours = v
		}
	}

	var results []store.GroupedStatItem
	var err error

	if dimension == "keyword" {
		results, err = h.logStore.GetGroupedErrorByKeyword(windowHours)
	} else {
		results, err = h.logStore.GetGroupedErrorStats(dimension, windowHours)
	}

	if err != nil {
		h.logger.Error("get grouped stats failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 渠道维度：将 channel_id 映射为渠道名称
	if dimension == "channel" && h.channelStore != nil {
		nameMap, mapErr := h.channelStore.GetChannelNameMap()
		if mapErr != nil {
			h.logger.Warn("get channel name map failed", zap.Error(mapErr))
		} else {
			for i := range results {
				if id, parseErr := strconv.ParseInt(results[i].GroupValue, 10, 64); parseErr == nil {
					if name, ok := nameMap[id]; ok && name != "" {
						results[i].GroupValue = name
					} else {
						results[i].GroupValue = "渠道#" + results[i].GroupValue
					}
				}
			}
		}
	}

	if results == nil {
		results = []store.GroupedStatItem{}
	}
	c.JSON(http.StatusOK, gin.H{"data": results})
}

func (h *ErrorScanHandler) GetGroupedSamples(c *gin.Context) {
	dimension := c.DefaultQuery("dimension", "channel")
	rawKey := c.Query("raw_key")
	if rawKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "raw_key is required"})
		return
	}
	windowHours := 1
	if wh := c.Query("window"); wh != "" {
		if v, err := strconv.Atoi(wh); err == nil && v > 0 {
			windowHours = v
		}
	}

	samples, err := h.logStore.GetGroupedErrorSamples(dimension, rawKey, windowHours, 50)
	if err != nil {
		h.logger.Error("get grouped samples failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if samples == nil {
		samples = []store.GroupedErrorDetail{}
	}
	c.JSON(http.StatusOK, gin.H{"data": samples})
}

// GetCacheHitRate 返回最近 windowHours 小时内按用户/渠道/渠道内模型维度的缓存命中率折线数据
func (h *ErrorScanHandler) GetCacheHitRate(c *gin.Context) {
	dimension := c.DefaultQuery("dimension", "user")
	if dimension != "user" && dimension != "channel" && dimension != "model" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dimension must be user, channel or model"})
		return
	}
	channelID := 0
	if v := c.Query("channel_id"); v != "" {
		channelID, _ = strconv.Atoi(v)
	}
	if dimension == "model" && channelID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dimension model requires channel_id"})
		return
	}
	window := 2
	if v := c.Query("window"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 24 {
			window = n
		}
	}

	result, err := h.logStore.GetCacheHitRate(dimension, channelID, window, 10)
	if err != nil {
		h.logger.Error("get cache hit rate failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 渠道维度：将 channel_id 映射为渠道名称
	if dimension == "channel" && h.channelStore != nil {
		nameMap, mapErr := h.channelStore.GetChannelNameMap()
		if mapErr != nil {
			h.logger.Warn("get channel name map failed", zap.Error(mapErr))
		} else {
			for i := range result.Series {
				if id, parseErr := strconv.ParseInt(result.Series[i].Name, 10, 64); parseErr == nil {
					if name, ok := nameMap[id]; ok && name != "" {
						result.Series[i].Name = name
					} else {
						result.Series[i].Name = "渠道#" + result.Series[i].Name
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"data": result})
}

// GetChannelErrorRate 返回最近 windowHours 小时内各渠道的错误率折线数据
// 错误率 = type=5 错误日志数 / 该渠道全部日志数
func (h *ErrorScanHandler) GetChannelErrorRate(c *gin.Context) {
	window := 2
	if v := c.Query("window"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 24 {
			window = n
		}
	}

	result, err := h.logStore.GetChannelErrorRate(window, 10)
	if err != nil {
		h.logger.Error("get channel error rate failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 将 channel_id 映射为渠道名称
	if h.channelStore != nil {
		nameMap, mapErr := h.channelStore.GetChannelNameMap()
		if mapErr != nil {
			h.logger.Warn("get channel name map failed", zap.Error(mapErr))
		} else {
			for i := range result.Series {
				if id, parseErr := strconv.ParseInt(result.Series[i].Name, 10, 64); parseErr == nil {
					if name, ok := nameMap[id]; ok && name != "" {
						result.Series[i].Name = name
					} else {
						result.Series[i].Name = "渠道#" + result.Series[i].Name
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"data": result})
}

// ClassifyLog 根据分类规则匹配一条错误日志
func (h *ErrorScanHandler) ClassifyLog(c *gin.Context) {
	var body struct {
		Content     string `json:"content"`
		Other       string `json:"other"`
		ModelName   string `json:"model_name"`
		ChannelName string `json:"channel_name"`
		Username    string `json:"username"`
		Group       string `json:"group"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	rules, err := h.classifyStore.List()
	if err != nil {
		h.logger.Error("list classify rules failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 解析 statusCode
	statusCode := 0
	if body.Other != "" {
		// 简单提取 status_code
		for _, part := range strings.Split(body.Other, ",") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "\"status_code\":") {
				if v, err := strconv.Atoi(strings.Trim(strings.TrimPrefix(part, "\"status_code\":"), "\"")); err == nil {
					statusCode = v
				}
			}
		}
	}

	// 按优先级匹配
	contentLower := strings.ToLower(body.Content)
	for _, rule := range rules {
		keywordLower := strings.ToLower(rule.Keyword)
		if !strings.Contains(contentLower, keywordLower) {
			continue
		}
		// 状态码过滤（0 表示不限）
		if rule.StatusCode != 0 && statusCode != 0 && rule.StatusCode != statusCode {
			continue
		}
		// 命中
		c.JSON(http.StatusOK, gin.H{"data": gin.H{
			"category":        rule.Category,
			"sub_category":    rule.SubCategory,
			"status_code":     statusCode,
			"keyword_matched": rule.Keyword,
			"recommendation":  rule.Recommendation,
			"model":           body.ModelName,
			"channel":         body.ChannelName,
			"username":        body.Username,
			"group":           body.Group,
		}})
		return
	}

	// 无匹配
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"category":        "其他参数/网络错误",
		"sub_category":    "未知错误",
		"status_code":     statusCode,
		"keyword_matched": "",
		"recommendation":  "未匹配到已有分类规则，建议查看 content 内容手动归类。",
		"model":           body.ModelName,
		"channel":         body.ChannelName,
		"username":        body.Username,
		"group":           body.Group,
	}})
}

// ── 分类规则管理 ──

func (h *ErrorScanHandler) ListClassifyRules(c *gin.Context) {
	rules, err := h.classifyStore.List()
	if err != nil {
		h.logger.Error("list classify rules failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rules == nil {
		rules = []store.ErrorClassifyRule{}
	}
	c.JSON(http.StatusOK, gin.H{"data": rules})
}

func (h *ErrorScanHandler) CreateClassifyRule(c *gin.Context) {
	var rule store.ErrorClassifyRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if rule.Keyword == "" || rule.Category == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "keyword and category are required"})
		return
	}

	// 关键词唯一：如果已存在同关键词的规则，则更新合并
	existingRules, _ := h.classifyStore.List()
	for _, cr := range existingRules {
		if strings.EqualFold(cr.Keyword, rule.Keyword) {
			// 合并：用新数据覆盖，如果状态码不同则设为0（匹配所有）
			if cr.StatusCode != rule.StatusCode && rule.StatusCode != 0 {
				rule.StatusCode = 0
			}
			rule.Id = cr.Id
			if err := h.classifyStore.Update(&rule); err != nil {
				h.logger.Error("update (merge) classify rule failed", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			h.logger.Info("merged classify rule (keyword exists)", zap.String("keyword", rule.Keyword))
			c.JSON(http.StatusOK, gin.H{"data": rule, "merged": true})
			return
		}
	}

	if err := h.classifyStore.Create(&rule); err != nil {
		h.logger.Error("create classify rule failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rule})
}

func (h *ErrorScanHandler) DeleteClassifyRule(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.classifyStore.Delete(id); err != nil {
		h.logger.Error("delete classify rule failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func (h *ErrorScanHandler) UpdateClassifyRule(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var rule store.ErrorClassifyRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rule.Id = id
	if err := h.classifyStore.Update(&rule); err != nil {
		h.logger.Error("update classify rule failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rule})
}
