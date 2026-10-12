package api

import (
	"tt-inspector/internal/config"
	"tt-inspector/internal/engine"
	"tt-inspector/internal/notifier"
	"tt-inspector/internal/probe"
	"tt-inspector/internal/store"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func RegisterRoutes(r *gin.Engine, ruleStore *store.RuleStore, errorScanStore *store.ErrorScanStore, logStore *store.LogStore, classifyStore *store.ErrorClassifyStore, channelStore *store.ChannelStore, alertEngine *engine.Engine, errorScanner *engine.ErrorScanner, probeStore *store.ProbeStore, probeScheduler *probe.Scheduler, feishuNotifier notifier.Notifier, logger *zap.Logger, authCfg config.AuthConfig, veridropCfg config.VeridropConfig) {
	ruleHandler := &RuleHandler{
		store:  ruleStore,
		engine: alertEngine,
		logger: logger,
	}

	errorScanHandler := &ErrorScanHandler{
		store:         errorScanStore,
		logStore:      logStore,
		classifyStore: classifyStore,
		channelStore:  channelStore,
		scanner:       errorScanner,
		logger:        logger,
	}

	channelHandler := &ChannelHandler{
		store: channelStore,
	}

	probeHandler := &ProbeHandler{
		store:     probeStore,
		scheduler: probeScheduler,
		notifier:  feishuNotifier,
		logger:    logger,
	}

	// 登录接口（无需认证）
	r.POST("/api/v1/login", LoginHandler(authCfg.JWTSecret, authCfg.AdminPassword))

	// 中转站检测（veridrop）反向代理：挂在 /veridrop 下，整组用 Cookie 鉴权
	if veridropCfg.Enabled {
		if err := RegisterVeridropRoutes(r, veridropCfg, authCfg.JWTSecret, logger); err != nil {
			logger.Error("failed to enable veridrop proxy", zap.Error(err))
		}
	}

	// 需要认证的 API
	api := r.Group("/api/v1")
	api.Use(AuthMiddleware(authCfg.JWTSecret))
	{
		api.GET("/check-auth", CheckAuthHandler())

		api.GET("/rules", ruleHandler.List)
		api.POST("/rules", ruleHandler.Create)
		api.GET("/rules/:id", ruleHandler.Get)
		api.PUT("/rules/:id", ruleHandler.Update)
		api.DELETE("/rules/:id", ruleHandler.Delete)
		api.POST("/rules/:id/enable", ruleHandler.Enable)
		api.POST("/rules/:id/disable", ruleHandler.Disable)

		api.POST("/rules/test-match", ruleHandler.TestMatch)

		api.GET("/channels", channelHandler.ListChannels)
		api.GET("/channels/with-models", channelHandler.ListChannelsWithModels)
		api.GET("/cache-hit-rate", errorScanHandler.GetCacheHitRate)
		api.GET("/error-rate", errorScanHandler.GetChannelErrorRate)

		// 错误扫描规则
		api.GET("/error-scan", errorScanHandler.List)
		api.POST("/error-scan", errorScanHandler.Create)
		api.PUT("/error-scan/:id", errorScanHandler.Update)
		api.DELETE("/error-scan/:id", errorScanHandler.Delete)
		api.POST("/error-scan/:id/toggle-alert", errorScanHandler.ToggleAlert)
		api.POST("/error-scan/:id/toggle-suppressed", errorScanHandler.ToggleSuppressed)
		api.POST("/error-scan/:id/granularity", errorScanHandler.UpdateGranularity)
		api.POST("/error-scan/:id/threshold-excludes", errorScanHandler.UpdateThresholdAndExcludes)
		api.GET("/error-scan/poll-interval", errorScanHandler.GetPollInterval)
		api.POST("/error-scan/poll-interval", errorScanHandler.SetPollInterval)
		api.POST("/error-scan/test-webhook", errorScanHandler.TestWebhook)
		api.POST("/error-scan/trigger-scan", errorScanHandler.TriggerScan)
		api.GET("/error-scan/recent-samples", errorScanHandler.GetRecentErrorSamples)
		api.GET("/error-scan/overview", errorScanHandler.GetOverview)
		api.GET("/error-scan/unmatched-samples", errorScanHandler.GetUnmatchedSamples)
		api.POST("/error-scan/create-from-sample", errorScanHandler.CreateFromSample)
		api.GET("/error-scan/fallback-config", errorScanHandler.GetFallbackConfig)
		api.POST("/error-scan/fallback-config", errorScanHandler.SetFallbackConfig)
		api.GET("/error-scan/alert-at-users", errorScanHandler.GetAlertAtUsers)
		api.POST("/error-scan/alert-at-users", errorScanHandler.SetAlertAtUsers)
		api.GET("/error-scan/feishu-app-config", errorScanHandler.GetFeishuAppConfig)
		api.POST("/error-scan/feishu-app-config", errorScanHandler.SetFeishuAppConfig)
		api.GET("/error-scan/grouped-stats", errorScanHandler.GetGroupedStats)
		api.GET("/error-scan/grouped-samples", errorScanHandler.GetGroupedSamples)
		api.POST("/error-scan/classify-log", errorScanHandler.ClassifyLog)
		api.GET("/error-scan/classify-rules", errorScanHandler.ListClassifyRules)
		api.POST("/error-scan/classify-rules", errorScanHandler.CreateClassifyRule)
		api.PUT("/error-scan/classify-rules/:id", errorScanHandler.UpdateClassifyRule)
		api.DELETE("/error-scan/classify-rules/:id", errorScanHandler.DeleteClassifyRule)

		// 渠道探测（可用性 + 质量）
		api.GET("/probe/config", probeHandler.GetConfig)
		api.PUT("/probe/config/availability", probeHandler.UpdateAvailabilityConfig)
		api.PUT("/probe/config/quality", probeHandler.UpdateQualityConfig)
		api.POST("/probe/validate-cron", probeHandler.ValidateCron)
		api.POST("/probe/availability/run", probeHandler.RunAvailability)
		api.POST("/probe/quality/run", probeHandler.RunQuality)
		api.GET("/probe/runs", probeHandler.ListRuns)
		api.GET("/probe/results", probeHandler.ListResults)
		api.POST("/probe/test-webhook", probeHandler.TestWebhook)
	}
}
