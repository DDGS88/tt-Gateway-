package engine

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"tt-inspector/internal/notifier"
	"tt-inspector/internal/store"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// keywordRegexSpecialChars 匹配正则特殊字符
var keywordRegexSpecialChars = regexp.MustCompile(`([\\.*+?^${}()|[\]{}])`)

// escapeKeyword 转义正则特殊字符，使关键词作为字面量匹配
func escapeKeyword(s string) string {
	return keywordRegexSpecialChars.ReplaceAllString(s, `\$1`)
}

// ErrorScanner 错误日志扫描调度器
type ErrorScanner struct {
	db              *gorm.DB
	logStore        *store.LogStore
	scanStore       *store.ErrorScanStore
	channelStore    *store.ChannelStore
	notifier        notifier.Notifier           // webhook 通知器，仅兜底告警使用
	appNotifier     *notifier.FeishuAppNotifier // 飞书自建应用通知器，日志告警统一走 app 模式
	logger          *zap.Logger
	pollInterval    time.Duration
	intervalMu      sync.RWMutex
	cooldowns       map[int64]map[string]time.Time // ruleId -> groupValue -> last alert time
	cooldownsMu     sync.Mutex
	cooldownSeconds int
	// 兜底告警
	fallbackEnabled  bool
	fallbackWebhook  string
	fallbackMu       sync.RWMutex
	fallbackCooldown time.Time
	// 全局告警@用户
	alertAtUsers []string
	atUsersMu    sync.RWMutex
	// 飞书自建应用配置（app 模式全局配置）
	feishuApp notifier.FeishuAppConfig
}

// NewErrorScanner 创建错误扫描器
func NewErrorScanner(
	db *gorm.DB,
	scanStore *store.ErrorScanStore,
	channelStore *store.ChannelStore,
	notifier notifier.Notifier,
	appNotifier *notifier.FeishuAppNotifier,
	logger *zap.Logger,
	pollInterval time.Duration,
	cooldownSeconds int,
) *ErrorScanner {
	s := &ErrorScanner{
		db:              db,
		logStore:        store.NewLogStore(db),
		scanStore:       scanStore,
		channelStore:    channelStore,
		notifier:        notifier,
		appNotifier:     appNotifier,
		logger:          logger,
		pollInterval:    pollInterval,
		cooldowns:       make(map[int64]map[string]time.Time),
		cooldownSeconds: cooldownSeconds,
	}
	// 加载持久化的全局配置
	if appID, _ := scanStore.GetSetting("feishu_app_id"); appID != "" {
		s.feishuApp.AppID = appID
	}
	if appSecret, _ := scanStore.GetSetting("feishu_app_secret"); appSecret != "" {
		s.feishuApp.AppSecret = appSecret
	}
	if chatID, _ := scanStore.GetSetting("feishu_chat_id"); chatID != "" {
		s.feishuApp.ChatID = chatID
	}
	if usersStr, _ := scanStore.GetSetting("alert_at_users"); usersStr != "" {
		s.alertAtUsers = strings.Split(usersStr, ",")
	}
	return s
}

// Run 启动扫描主循环
func (s *ErrorScanner) Run(ctx context.Context) {
	s.logger.Info("error scanner started",
		zap.Duration("poll_interval", s.pollInterval),
		zap.Int("cooldown_seconds", s.cooldownSeconds))

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("error scanner stopped")
			return
		case <-time.After(s.GetPollInterval()):
			s.scanAll()
			s.checkFallbackAlert()
		}
	}
}

// GetPollInterval 获取当前轮询间隔
func (s *ErrorScanner) GetPollInterval() time.Duration {
	s.intervalMu.RLock()
	defer s.intervalMu.RUnlock()
	return s.pollInterval
}

// SetPollInterval 动态设置轮询间隔（秒）
func (s *ErrorScanner) SetPollInterval(seconds int) {
	s.intervalMu.Lock()
	defer s.intervalMu.Unlock()
	s.pollInterval = time.Duration(seconds) * time.Second
	s.logger.Info("error scan poll interval updated",
		zap.Int("seconds", seconds))
}

// TriggerScan 手动触发一次扫描（调试用）
func (s *ErrorScanner) TriggerScan() {
	s.logger.Info("manual scan triggered")
	go s.scanAll()
}

// SetFallbackConfig 配置兜底告警
func (s *ErrorScanner) SetFallbackConfig(enabled bool, webhook string) {
	s.fallbackMu.Lock()
	defer s.fallbackMu.Unlock()
	s.fallbackEnabled = enabled
	s.fallbackWebhook = webhook
	s.logger.Info("fallback config updated", zap.Bool("enabled", enabled))
}

// GetFallbackConfig 获取兜底告警配置
func (s *ErrorScanner) GetFallbackConfig() (bool, string) {
	s.fallbackMu.RLock()
	defer s.fallbackMu.RUnlock()
	return s.fallbackEnabled, s.fallbackWebhook
}

// SetAlertAtUsers 设置全局告警@用户
func (s *ErrorScanner) SetAlertAtUsers(users []string) {
	s.atUsersMu.Lock()
	defer s.atUsersMu.Unlock()
	s.alertAtUsers = users
	// 持久化
	if s.scanStore != nil {
		_ = s.scanStore.SetSetting("alert_at_users", strings.Join(users, ","))
	}
}

// GetAlertAtUsers 获取全局告警@用户
func (s *ErrorScanner) GetAlertAtUsers() []string {
	s.atUsersMu.RLock()
	defer s.atUsersMu.RUnlock()
	return s.alertAtUsers
}

// SetFeishuAppConfig 设置飞书自建应用配置（app 模式全局配置），并持久化
func (s *ErrorScanner) SetFeishuAppConfig(cfg notifier.FeishuAppConfig) {
	s.atUsersMu.Lock()
	defer s.atUsersMu.Unlock()
	s.feishuApp = cfg
	if s.scanStore != nil {
		_ = s.scanStore.SetSetting("feishu_app_id", cfg.AppID)
		_ = s.scanStore.SetSetting("feishu_app_secret", cfg.AppSecret)
		_ = s.scanStore.SetSetting("feishu_chat_id", cfg.ChatID)
	}
}

// GetFeishuAppConfig 获取飞书自建应用配置
func (s *ErrorScanner) GetFeishuAppConfig() notifier.FeishuAppConfig {
	s.atUsersMu.RLock()
	defer s.atUsersMu.RUnlock()
	return s.feishuApp
}

// checkFallbackAlert 检查是否需要发送兜底告警
func (s *ErrorScanner) checkFallbackAlert() {
	s.fallbackMu.RLock()
	enabled := s.fallbackEnabled
	webhook := s.fallbackWebhook
	s.fallbackMu.RUnlock()

	if !enabled {
		return
	}

	// 冷却检查
	if time.Since(s.fallbackCooldown) < time.Duration(s.cooldownSeconds)*time.Second {
		return
	}

	rules, _ := s.scanStore.List()
	patterns := make([]string, 0)
	for _, r := range rules {
		if r.PatternKeyword != "" {
			patterns = append(patterns, r.PatternKeyword)
		}
	}

	stats, err := s.logStore.GetOverviewStats(patterns, 1)
	if err != nil {
		return
	}

	if stats.UnmatchedErrors > 0 {
		s.fallbackCooldown = time.Now()

		msg := notifier.AlertMessage{
			RuleName:    "[兜底告警] 未匹配的错误日志",
			RuleType:    "fallback",
			MatchCount:  int(stats.UnmatchedErrors),
			Threshold:   0,
			WindowStart: time.Now().Add(-time.Hour),
			WindowEnd:   time.Now(),
			Condition:   "type=5 未命中任何规则",
			Description: fmt.Sprintf("当前有 %d 条 type=5 错误日志未被任何精细化规则覆盖，总匹配率 %.1f%%，请及时添加规则",
				stats.UnmatchedErrors, stats.MatchRate),
		}

		// 填写了独立 Webhook：走 webhook 通道单独发送（不 @ 用户）
		if webhook != "" {
			if err := s.notifier.Send(webhook, msg); err != nil {
				s.logger.Error("fallback alert failed", zap.Error(err))
			} else {
				s.logger.Warn("fallback alert sent",
					zap.Int64("unmatched", stats.UnmatchedErrors),
					zap.Float64("match_rate", stats.MatchRate))
			}
			return
		}

		// 未填写 Webhook：按默认方式走飞书应用（app 模式）告警
		cfg := s.GetFeishuAppConfig()
		if cfg.ChatID == "" {
			s.logger.Warn("fallback alert skipped: no fallback webhook and feishu app chat_id empty")
			return
		}
		msg.AtUsers = s.GetAlertAtUsers()
		if err := s.appNotifier.SendCard(cfg, msg); err != nil {
			s.logger.Error("fallback alert failed", zap.Error(err))
		} else {
			s.logger.Warn("fallback alert sent via feishu app",
				zap.Int64("unmatched", stats.UnmatchedErrors),
				zap.Float64("match_rate", stats.MatchRate))
		}
	}
}

// TestFeishuApp 测试飞书自建应用配置是否可用（app 模式发送测试卡片）
func (s *ErrorScanner) TestFeishuApp(cfg notifier.FeishuAppConfig) error {
	msg := notifier.AlertMessage{
		RuleName:    "[错误扫描] 飞书应用连通性测试",
		RuleType:    "测试",
		MatchCount:  0,
		Threshold:   0,
		WindowStart: time.Now(),
		WindowEnd:   time.Now(),
		Condition:   "手动测试",
		Description: "这是一条来自 tt-inspector 错误扫描的飞书应用机器人连通性测试消息，如果你收到此消息说明配置正确。",
		AtUsers:     s.GetAlertAtUsers(),
	}
	err := s.appNotifier.SendCard(cfg, msg)
	if err != nil {
		s.logger.Error("feishu app test failed",
			zap.String("app_id", truncateURL(cfg.AppID)),
			zap.Error(err))
	}
	return err
}

// scanAll 一次查询窗口内错误日志，内存中批量匹配所有启用的规则
func (s *ErrorScanner) scanAll() {
	rules, err := s.scanStore.List()
	if err != nil {
		s.logger.Error("failed to load error scan rules", zap.Error(err))
		return
	}

	// 过滤出启用且有关键词的规则
	var activeRules []*store.ErrorScanRule
	for i := range rules {
		rule := &rules[i]
		if !rule.IsAlertEnabled || rule.Suppressed {
			continue
		}
		if rule.PatternKeyword == "" {
			continue
		}
		activeRules = append(activeRules, rule)
	}
	if len(activeRules) == 0 {
		return
	}

	now := time.Now()
	windowEnd := now.Unix()
	pollSec := int64(s.GetPollInterval().Seconds())

	// 按扫描粒度分窗口，分别只查询一次日志
	var hourlyRules, dailyRules []*store.ErrorScanRule
	for _, rule := range activeRules {
		if rule.ScanGranularity == "天 (Daily)" {
			dailyRules = append(dailyRules, rule)
		} else {
			hourlyRules = append(hourlyRules, rule)
		}
	}

	if len(hourlyRules) > 0 {
		s.scanRulesInMemory(hourlyRules, windowEnd-pollSec, windowEnd)
	}
	if len(dailyRules) > 0 {
		s.scanRulesInMemory(dailyRules, windowEnd-86400, windowEnd)
	}
}

// scanRulesInMemory 对一批规则，在已查询的日志切片上做内存匹配
func (s *ErrorScanner) scanRulesInMemory(rules []*store.ErrorScanRule, windowStart, windowEnd int64) {
	logs, err := s.logStore.GetErrorLogsForScan(windowStart, windowEnd)
	if err != nil {
		s.logger.Error("failed to load error logs for scan", zap.Error(err))
		return
	}

	for _, rule := range rules {
		keyword := strings.ToLower(rule.PatternKeyword)

		// 按错误精细类型：直接统计总数，不分组
		if rule.AlertGranularity == "按错误精细类型" {
			totalCount := 0
			for i := range logs {
				if strings.Contains(strings.ToLower(logs[i].Content), keyword) {
					totalCount++
				}
			}

			s.logger.Debug("scan rule result",
				zap.Int64("rule_id", rule.Id),
				zap.String("name", rule.Name),
				zap.String("keyword", rule.PatternKeyword),
				zap.Int("count", totalCount),
				zap.Int("threshold", rule.AlertThreshold))

			s.updateCounts(rule, totalCount)

			if totalCount >= rule.AlertThreshold {
				s.sendAlert(rule, "总览", totalCount, windowStart, windowEnd)
			}
			continue
		}

		// 按维度分组统计
		groupCounts := make(map[string]int)
		for i := range logs {
			if !strings.Contains(strings.ToLower(logs[i].Content), keyword) {
				continue
			}
			groupCounts[s.groupValueOf(rule.AlertGranularity, &logs[i])]++
		}

		s.logger.Debug("scan rule grouped result",
			zap.Int64("rule_id", rule.Id),
			zap.String("name", rule.Name),
			zap.String("keyword", rule.PatternKeyword),
			zap.String("granularity", rule.AlertGranularity),
			zap.Int("groups", len(groupCounts)),
			zap.Int("threshold", rule.AlertThreshold))

		// 构建排除集合
		filterSet := s.buildExcludeSet(rule)
		hasFilter := len(filterSet) > 0

		// 按 count 降序排序，保持告警顺序稳定
		type groupEntry struct {
			value string
			count int
		}
		entries := make([]groupEntry, 0, len(groupCounts))
		for v, c := range groupCounts {
			entries = append(entries, groupEntry{value: v, count: c})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].count > entries[j].count })

		// 累计总数
		totalCount := 0
		for _, e := range entries {
			if s.shouldSkip(e.value, filterSet, rule.FilterMode, hasFilter) {
				continue
			}

			totalCount += e.count

			if e.count >= rule.AlertThreshold {
				s.sendGroupAlert(rule, e.value, e.count, windowStart, windowEnd)
			}
		}

		s.updateCounts(rule, totalCount)
	}
}

// groupValueOf 根据告警粒度从日志条目中提取分组值
func (s *ErrorScanner) groupValueOf(granularity string, lg *store.ScanLogEntry) string {
	switch granularity {
	case "按模型":
		return lg.ModelName
	case "按渠道":
		return lg.ChannelName
	case "按用户":
		return lg.Username
	default:
		return ""
	}
}

// buildExcludeSet 构建排除集合（filterMode=include时反转为选中集合）
func (s *ErrorScanner) buildExcludeSet(rule *store.ErrorScanRule) map[string]struct{} {
	targetSet := make(map[string]struct{})

	var filterList string
	if rule.AlertGranularity == "按渠道" {
		filterList = rule.ExcludeChannels
	} else if rule.AlertGranularity == "按用户" {
		filterList = rule.ExcludeUsers
	}

	if filterList != "" {
		for _, v := range strings.Split(filterList, ",") {
			v = strings.TrimSpace(v)
			if v != "" {
				targetSet[v] = struct{}{}
			}
		}
	}
	return targetSet
}

// shouldSkip 判断分组值是否需要跳过
func (s *ErrorScanner) shouldSkip(groupValue string, filterSet map[string]struct{}, filterMode string, hasFilter bool) bool {
	if !hasFilter {
		return false
	}
	_, inSet := filterSet[groupValue]
	if filterMode == "include" {
		// include模式：集合里没有的要跳过
		return !inSet
	}
	// exclude模式：集合里的要跳过
	return inSet
}

// updateCounts 更新错误计数到数据库
func (s *ErrorScanner) updateCounts(rule *store.ErrorScanRule, count int) {
	if rule.ScanGranularity == "天 (Daily)" {
		if err := s.scanStore.UpdateDailyCount(rule.Id, count); err != nil {
			s.logger.Warn("update daily count failed", zap.Int64("rule_id", rule.Id), zap.Error(err))
		}
	} else {
		if err := s.scanStore.UpdateHourlyCount(rule.Id, count); err != nil {
			s.logger.Warn("update hourly count failed", zap.Int64("rule_id", rule.Id), zap.Error(err))
		}
	}
}

// sendAlert 发送不分组告警（app 模式，发送到全局配置的飞书群）
func (s *ErrorScanner) sendAlert(rule *store.ErrorScanRule, groupValue string, count int, windowStart, windowEnd int64) {
	cfg := s.GetFeishuAppConfig()
	if cfg.ChatID == "" {
		return
	}

	// 冷却检查
	cooldownKey := rule.Id
	if !s.checkCooldown(cooldownKey, "") {
		return
	}

	msg := notifier.AlertMessage{
		RuleName:    fmt.Sprintf("[错误扫描] %s", rule.Name),
		RuleType:    rule.Category,
		MatchCount:  count,
		Threshold:   rule.AlertThreshold,
		WindowStart: time.Unix(windowStart, 0),
		WindowEnd:   time.Unix(windowEnd, 0),
		Condition:   fmt.Sprintf("关键词: %s | 粒度: %s", rule.PatternKeyword, rule.AlertGranularity),
		Description: fmt.Sprintf("%s 本周期共出现 %d 次错误", rule.SubCategory, count),
		AtUsers:     s.GetAlertAtUsers(),
	}

	// 获取匹配样本，融合 content、渠道、模型、request_id 到卡片
	samples, err := s.logStore.GetRegexSamples(windowStart, windowEnd,
		0, "content", escapeKeyword(rule.PatternKeyword), "", false, false, 5)
	if err != nil {
		s.logger.Warn("fetch alert samples failed", zap.Int64("rule_id", rule.Id), zap.Error(err))
	} else {
		channelNameMap := s.getChannelNameMap()
		msg.Samples = make([]notifier.LogSampleItem, 0, len(samples))
		for _, sm := range samples {
			msg.Samples = append(msg.Samples, notifier.LogSampleItem{
				MatchedValue: sm.MatchedValue,
				ResponseID:   sm.ResponseID,
				RequestID:    sm.RequestID,
				ChannelName:  channelNameMap[int64(sm.ChannelId)],
				ModelName:    sm.ModelName,
				IsStream:     sm.IsStream,
			})
		}
	}

	if err := s.appNotifier.SendCard(cfg, msg); err != nil {
		s.logger.Error("send error alert failed",
			zap.Int64("rule_id", rule.Id), zap.Error(err))
		return
	}

	s.logger.Info("error alert sent",
		zap.Int64("rule_id", rule.Id),
		zap.String("name", rule.Name),
		zap.Int("count", count))
}

// sendGroupAlert 发送分组告警（app 模式，发送到全局配置的飞书群）
func (s *ErrorScanner) sendGroupAlert(rule *store.ErrorScanRule, groupValue string, count int, windowStart, windowEnd int64) {
	cfg := s.GetFeishuAppConfig()
	if cfg.ChatID == "" {
		return
	}

	// 冷却检查（按 ruleId + groupValue）
	if !s.checkCooldown(rule.Id, groupValue) {
		return
	}

	msg := notifier.AlertMessage{
		RuleName:    fmt.Sprintf("[错误扫描] %s - %s", rule.Name, groupValue),
		RuleType:    rule.Category,
		MatchCount:  count,
		Threshold:   rule.AlertThreshold,
		WindowStart: time.Unix(windowStart, 0),
		WindowEnd:   time.Unix(windowEnd, 0),
		Condition:   fmt.Sprintf("关键词: %s | %s: %s", rule.PatternKeyword, rule.AlertGranularity, groupValue),
		Description: fmt.Sprintf("%s(%s) 本周期出现 %d 次错误，超过阈值 %d",
			rule.SubCategory, groupValue, count, rule.AlertThreshold),
		AtUsers: s.GetAlertAtUsers(),
	}

	// 获取匹配样本，融合 content、渠道、模型、request_id 到卡片
	samples, err := s.logStore.GetRegexSamples(windowStart, windowEnd,
		0, "content", escapeKeyword(rule.PatternKeyword), "", false, false, 5)
	if err != nil {
		s.logger.Warn("fetch group alert samples failed", zap.Int64("rule_id", rule.Id), zap.Error(err))
	} else {
		channelNameMap := s.getChannelNameMap()
		msg.Samples = make([]notifier.LogSampleItem, 0, len(samples))
		for _, sm := range samples {
			msg.Samples = append(msg.Samples, notifier.LogSampleItem{
				MatchedValue: sm.MatchedValue,
				ResponseID:   sm.ResponseID,
				RequestID:    sm.RequestID,
				ChannelName:  channelNameMap[int64(sm.ChannelId)],
				ModelName:    sm.ModelName,
				IsStream:     sm.IsStream,
			})
		}
	}

	if err := s.appNotifier.SendCard(cfg, msg); err != nil {
		s.logger.Error("send group error alert failed",
			zap.Int64("rule_id", rule.Id),
			zap.String("group_value", groupValue),
			zap.Error(err))
		return
	}

	s.logger.Info("group error alert sent",
		zap.Int64("rule_id", rule.Id),
		zap.String("group_value", groupValue),
		zap.Int("count", count))
}

// getChannelNameMap 获取渠道 id → 名称映射（失败时返回空 map）
func (s *ErrorScanner) getChannelNameMap() map[int64]string {
	if s.channelStore == nil {
		return map[int64]string{}
	}
	m, err := s.channelStore.GetChannelNameMap()
	if err != nil {
		s.logger.Warn("get channel name map failed", zap.Error(err))
		return map[int64]string{}
	}
	return m
}

// checkCooldown 检查冷却期，返回 true 表示可以发送
func (s *ErrorScanner) checkCooldown(ruleId int64, groupValue string) bool {
	s.cooldownsMu.Lock()
	defer s.cooldownsMu.Unlock()

	if ruleGroups, ok := s.cooldowns[ruleId]; ok {
		if lastAlert, exists := ruleGroups[groupValue]; exists {
			if time.Since(lastAlert) < time.Duration(s.cooldownSeconds)*time.Second {
				return false
			}
		}
	}
	if s.cooldowns[ruleId] == nil {
		s.cooldowns[ruleId] = make(map[string]time.Time)
	}
	s.cooldowns[ruleId][groupValue] = time.Now()
	return true
}

// truncateURL 截断 webhook URL 用于日志显示
func truncateURL(url string) string {
	if url == "" {
		return "(empty)"
	}
	if len(url) > 50 {
		return url[:50] + "..."
	}
	return url
}
