package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"tt-inspector/internal/api"
	"tt-inspector/internal/config"
	"tt-inspector/internal/engine"
	"tt-inspector/internal/notifier"
	"tt-inspector/internal/probe"
	"tt-inspector/internal/store"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// openDB 根据配置打开数据库连接
func openDB(cfg config.DatabaseConfig, name string, logger *zap.Logger) (*gorm.DB, error) {
	var dialector gorm.Dialector
	switch cfg.Driver {
	case "postgres", "postgresql", "pg":
		dialector = postgres.Open(cfg.DSN)
		logger.Info("using PostgreSQL driver", zap.String("db", name))
	default:
		dialector = mysql.Open(cfg.DSN)
		logger.Info("using MySQL driver", zap.String("db", name))
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("connect %s database: %w", name, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB for %s: %w", name, err)
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Hour)

	return db, nil
}

func Run(cfg *config.Config) error {
	zapCfg := zap.NewProductionConfig()
	if cfg.Logging.Format == "console" {
		zapCfg = zap.NewDevelopmentConfig()
	}
	level, err := zapcore.ParseLevel(cfg.Logging.Level)
	if err != nil {
		level = zapcore.InfoLevel
	}
	zapCfg.Level = zap.NewAtomicLevelAt(level)
	logger, err := zapCfg.Build()
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer logger.Sync()

	logger.Info("starting tt-inspector alert service")

	// business DB: 渠道 / 模型查询
	businessDB, err := openDB(cfg.Database, "business", logger)
	if err != nil {
		return err
	}

	// log DB: 告警规则存储 + 日志查询
	logDB, err := openDB(cfg.LogDatabase, "log", logger)
	if err != nil {
		return err
	}

	// 仅在 logDB 上迁移告警规则表
	if err := logDB.AutoMigrate(&store.AlertRule{}); err != nil {
		return fmt.Errorf("auto migrate alert_rules: %w", err)
	}
	if err := logDB.AutoMigrate(&store.ErrorScanRule{}); err != nil {
		return fmt.Errorf("auto migrate error_scan_rules: %w", err)
	}
	if err := logDB.AutoMigrate(&store.ErrorClassifyRule{}); err != nil {
		return fmt.Errorf("auto migrate error_classify_rules: %w", err)
	}
	// 数据迁移：将历史分类规则的 status_code 统一设为 0（匹配全部状态码）
	if result := logDB.Model(&store.ErrorClassifyRule{}).Where("status_code != ?", 0).
		Update("status_code", 0); result.Error != nil {
		logger.Warn("migrate classify rules status_code to 0 failed", zap.Error(result.Error))
	} else if result.RowsAffected > 0 {
		logger.Info("migrated classify rules status_code to 0", zap.Int64("affected_rows", result.RowsAffected))
	}
	if err := logDB.AutoMigrate(&store.ScanSetting{}); err != nil {
		logger.Warn("failed to migrate scan_settings table", zap.Error(err))
	}

	// 渠道探测相关表
	// 与 scan_settings 一致：建表失败不阻断启动，避免探测表的 DDL 异常导致整个服务起不来，
	// 此时渠道探测页面会因缺表而报错，日志里有明确记录可排查。
	if err := logDB.AutoMigrate(&store.ProbeSetting{}, &store.ProbeRun{}, &store.ProbeResult{}); err != nil {
		logger.Error("failed to migrate probe tables, 渠道探测功能将不可用", zap.Error(err))
	}

	// stores: rule + log 用 logDB，channel 用 businessDB
	ruleStore := store.NewRuleStore(logDB)
	errorScanStore := store.NewErrorScanStore(logDB)
	classifyStore := store.NewErrorClassifyStore(logDB)
	logStore := store.NewLogStore(logDB)
	channelStore := store.NewChannelStore(businessDB)
	probeStore := store.NewProbeStore(logDB)
	feishuNotifier := notifier.NewFeishuNotifier(logger)
	feishuAppNotifier := notifier.NewFeishuAppNotifier(logger)
	alertEngine := engine.NewEngine(logDB, ruleStore, logStore, feishuNotifier, cfg.Engine, logger)

	errorScanner := engine.NewErrorScanner(logDB, errorScanStore, channelStore, feishuNotifier, feishuAppNotifier, logger,
		cfg.Engine.ErrorScanPollInterval, cfg.Engine.DefaultCooldownSec)

	// 渠道探测（可用性 + 质量）
	availabilityProber := probe.NewAvailabilityProber(channelStore, probeStore, feishuNotifier, logger)
	veridropClient := probe.NewVeridropClient(cfg.Veridrop.Upstream, logger)
	qualityProber := probe.NewQualityProber(channelStore, probeStore, veridropClient, feishuNotifier, logger)
	probeScheduler := probe.NewScheduler(probeStore, availabilityProber, qualityProber, cfg.Probe, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go alertEngine.Run(ctx)
	go errorScanner.Run(ctx)
	probeScheduler.Start(ctx)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	// 前端静态资源与 JSON 响应体积较大，开启 gzip 传输压缩（woff/图片本身已压缩，跳过）
	r.Use(gzip.Gzip(gzip.DefaultCompression,
		gzip.WithExcludedExtensions([]string{".png", ".gif", ".jpeg", ".jpg", ".ico", ".woff", ".woff2"}),
		gzip.WithExcludedPaths([]string{"/veridrop"}),
	))
	// 构建产物文件名带内容哈希，可长期强缓存，避免每次访问重复下载
	r.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/assets/") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		c.Next()
	})
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	api.RegisterRoutes(r, ruleStore, errorScanStore, logStore, classifyStore, channelStore, alertEngine, errorScanner, probeStore, probeScheduler, feishuNotifier, logger, cfg.Auth, cfg.Veridrop)

	if _, err := os.Stat("ai-gateway-console/dist"); err == nil {
		r.Static("/assets", "ai-gateway-console/dist/assets")
		r.NoRoute(func(c *gin.Context) {
			c.File("ai-gateway-console/dist/index.html")
		})
		logger.Info("serving frontend static files from ai-gateway-console/dist")
	}

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	go func() {
		logger.Info("http server listening", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server listen failed", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	sig := <-quit
	logger.Info("received shutdown signal", zap.String("signal", sig.String()))

	cancel()
	alertEngine.Shutdown()
	probeScheduler.Stop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}

	logger.Info("server stopped gracefully")
	return nil
}
