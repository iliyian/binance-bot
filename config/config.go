package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config 应用配置结构
type Config struct {
	// Binance API
	BinanceAPIKey    string
	BinanceSecretKey string

	// 定投配置
	TradePairs   []string // 交易对列表
	TradeAmounts []string // 对应金额列表 (字符串保留精度)
	CronSchedule string   // cron 表达式
	Timezone     *time.Location

	// Telegram 通知
	TelegramBotToken string
	TelegramChatID   string

	// 可选
	UseDemo        bool   // 使用 demo.binance.com 模拟交易
	UseTestnet     bool   // 使用 testnet.binance.vision 测试网
	BinanceBaseURL string // 自定义 API 地址，最高优先级
	AutoEarn       bool   // 自动与活期理财互转
	LogLevel       string
}

// Load 从 .env 文件加载配置
func Load() (*Config, error) {
	// 尝试加载 .env 文件，不存在也不报错（允许纯环境变量方式）
	_ = godotenv.Load()

	cfg := &Config{}

	// 必填项
	cfg.BinanceAPIKey = os.Getenv("BINANCE_API_KEY")
	if cfg.BinanceAPIKey == "" {
		return nil, fmt.Errorf("BINANCE_API_KEY 未配置")
	}

	cfg.BinanceSecretKey = os.Getenv("BINANCE_SECRET_KEY")
	if cfg.BinanceSecretKey == "" {
		return nil, fmt.Errorf("BINANCE_SECRET_KEY 未配置")
	}

	// 交易对
	pairs := os.Getenv("TRADE_PAIRS")
	if pairs == "" {
		return nil, fmt.Errorf("TRADE_PAIRS 未配置")
	}
	cfg.TradePairs = strings.Split(pairs, ",")
	for i := range cfg.TradePairs {
		cfg.TradePairs[i] = strings.TrimSpace(cfg.TradePairs[i])
	}

	// 交易金额
	amounts := os.Getenv("TRADE_AMOUNTS")
	if amounts == "" {
		return nil, fmt.Errorf("TRADE_AMOUNTS 未配置")
	}
	cfg.TradeAmounts = strings.Split(amounts, ",")
	for i := range cfg.TradeAmounts {
		cfg.TradeAmounts[i] = strings.TrimSpace(cfg.TradeAmounts[i])
	}

	if len(cfg.TradePairs) != len(cfg.TradeAmounts) {
		return nil, fmt.Errorf("TRADE_PAIRS 和 TRADE_AMOUNTS 数量不匹配: %d vs %d",
			len(cfg.TradePairs), len(cfg.TradeAmounts))
	}

	// cron 表达式
	cfg.CronSchedule = os.Getenv("CRON_SCHEDULE")
	if cfg.CronSchedule == "" {
		cfg.CronSchedule = "0 0 9 * * *" // 默认每天 09:00
	}

	// 时区
	tz := os.Getenv("TIMEZONE")
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("无效的时区 %s: %w", tz, err)
	}
	cfg.Timezone = loc

	// Telegram
	cfg.TelegramBotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	cfg.TelegramChatID = os.Getenv("TELEGRAM_CHAT_ID")

	// 可选配置
	cfg.UseDemo = strings.ToLower(os.Getenv("USE_DEMO")) == "true"
	cfg.UseTestnet = strings.ToLower(os.Getenv("USE_TESTNET")) == "true"
	cfg.BinanceBaseURL = os.Getenv("BINANCE_BASE_URL")
	cfg.AutoEarn = strings.ToLower(os.Getenv("AUTO_EARN")) == "true"

	cfg.LogLevel = os.Getenv("LOG_LEVEL")
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}

	// 配置冲突检查
	if cfg.AutoEarn && (cfg.UseDemo || cfg.UseTestnet) {
		return nil, fmt.Errorf("AUTO_EARN 不能与 USE_DEMO 或 USE_TESTNET 同时启用（模拟/测试环境不支持理财 API）")
	}

	return cfg, nil
}

// HasTelegram 检查是否配置了 Telegram 通知
func (c *Config) HasTelegram() bool {
	return c.TelegramBotToken != "" && c.TelegramChatID != ""
}
