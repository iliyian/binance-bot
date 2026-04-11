package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config 应用配置结构
type Config struct {
	// Binance API
	BinanceAPIKey    string
	BinanceSecretKey string

	// Demo 专用 API（可选，USE_DEMO=true 时优先使用）
	BinanceDemoAPIKey    string
	BinanceDemoSecretKey string

	// 定投配置
	TradePairs   []string   // 交易对列表
	TradeAmounts []string   // 对应金额列表 (字符串保留精度)
	PoolAmounts  []float64  // 每个交易对的初始 pool 值 (用于重启恢复)
	CronSchedule string     // cron 表达式
	Timezone     *time.Location

	// Telegram 通知
	TelegramBotToken string
	TelegramChatID   string

	// 可选
	UseDemo        bool   // 使用 demo.binance.com 模拟交易
	BinanceBaseURL string // 自定义 API 地址，最高优先级
	AutoEarn       bool   // 自动与活期理财互转
	LogLevel       string

	// 布林带价格监控
	BollMonitorEnabled       bool
	BollMonitorSymbols       []BollMonitorSymbolConfig // 监控的交易对及其 K 线级别
	BollMonitorPeriod        int                       // 布林带周期，默认 20
	BollMonitorStdDev        float64                   // 布林带标准差倍数，默认 2.0
	BollMonitorCheckInterval time.Duration             // 检查间隔，默认 1m
}

// BollMonitorSymbolConfig 单个监控交易对配置
type BollMonitorSymbolConfig struct {
	Symbol    string   // 交易对，如 BTCUSDT
	Intervals []string // K 线级别列表，必须全部突破才触发，如 ["1h", "4h"]
}

// Load 从 .env 文件加载配置
func Load() (*Config, error) {
	// 尝试加载 .env 文件，不存在也不报错（允许纯环境变量方式）
	_ = godotenv.Load()

	cfg := &Config{}

	// 先加载模式标志，决定需要哪组密钥
	cfg.UseDemo = strings.ToLower(os.Getenv("USE_DEMO")) == "true"

	// API 密钥
	cfg.BinanceAPIKey = os.Getenv("BINANCE_API_KEY")
	cfg.BinanceSecretKey = os.Getenv("BINANCE_SECRET_KEY")
	cfg.BinanceDemoAPIKey = os.Getenv("BINANCE_DEMO_API_KEY")
	cfg.BinanceDemoSecretKey = os.Getenv("BINANCE_DEMO_SECRET_KEY")

	// 验证：根据模式要求对应密钥
	if cfg.UseDemo {
		if cfg.BinanceDemoAPIKey == "" {
			return nil, fmt.Errorf("BINANCE_DEMO_API_KEY 未配置")
		}
		if cfg.BinanceDemoSecretKey == "" {
			return nil, fmt.Errorf("BINANCE_DEMO_SECRET_KEY 未配置")
		}
	} else {
		if cfg.BinanceAPIKey == "" {
			return nil, fmt.Errorf("BINANCE_API_KEY 未配置")
		}
		if cfg.BinanceSecretKey == "" {
			return nil, fmt.Errorf("BINANCE_SECRET_KEY 未配置")
		}
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
	cfg.BinanceBaseURL = os.Getenv("BINANCE_BASE_URL")
	cfg.AutoEarn = strings.ToLower(os.Getenv("AUTO_EARN")) == "true"

	cfg.LogLevel = os.Getenv("LOG_LEVEL")
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}

	// 初始 Pool 金额（可选，用于重启后恢复累积的 pool）
	poolStr := os.Getenv("POOL_AMOUNTS")
	cfg.PoolAmounts = make([]float64, len(cfg.TradePairs))
	if poolStr != "" {
		poolParts := strings.Split(poolStr, ",")
		for i, p := range poolParts {
			if i >= len(cfg.TradePairs) {
				break
			}
			val, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
			if err != nil {
				return nil, fmt.Errorf("POOL_AMOUNTS 第 %d 个值无效: %s", i+1, p)
			}
			cfg.PoolAmounts[i] = val
		}
	}

	// 配置冲突检查
	if cfg.AutoEarn && cfg.UseDemo {
		return nil, fmt.Errorf("AUTO_EARN 不能与 USE_DEMO 同时启用（模拟环境不支持理财 API）")
	}

	// 布林带价格监控配置
	cfg.BollMonitorEnabled = strings.ToLower(os.Getenv("BOLL_MONITOR_ENABLED")) == "true"
	if cfg.BollMonitorEnabled {
		// 解析监控交易对，格式: BTCUSDT:1h&4h,ETHUSDT:15m&1h 或 BTCUSDT,ETHUSDT（使用统一 intervals）
		monSymbols := os.Getenv("BOLL_MONITOR_SYMBOLS")
		if monSymbols == "" {
			return nil, fmt.Errorf("BOLL_MONITOR_ENABLED=true 但 BOLL_MONITOR_SYMBOLS 未配置")
		}

		defaultIntervals := os.Getenv("BOLL_MONITOR_INTERVALS") // 全局默认，如 1h&4h

		for _, part := range strings.Split(monSymbols, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			sc := BollMonitorSymbolConfig{}
			if idx := strings.Index(part, ":"); idx >= 0 {
				// 格式: BTCUSDT:1h&4h
				sc.Symbol = strings.TrimSpace(part[:idx])
				sc.Intervals = strings.Split(strings.TrimSpace(part[idx+1:]), "&")
			} else {
				// 格式: BTCUSDT，使用全局默认 intervals
				sc.Symbol = part
				if defaultIntervals == "" {
					return nil, fmt.Errorf("BOLL_MONITOR_SYMBOLS 中 %s 未指定 K 线级别，且 BOLL_MONITOR_INTERVALS 未配置", part)
				}
				sc.Intervals = strings.Split(defaultIntervals, "&")
			}
			for i := range sc.Intervals {
				sc.Intervals[i] = strings.TrimSpace(sc.Intervals[i])
			}
			cfg.BollMonitorSymbols = append(cfg.BollMonitorSymbols, sc)
		}

		// 布林带参数
		cfg.BollMonitorPeriod = 20
		if v := os.Getenv("BOLL_MONITOR_PERIOD"); v != "" {
			p, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("BOLL_MONITOR_PERIOD 无效: %s", v)
			}
			cfg.BollMonitorPeriod = p
		}

		cfg.BollMonitorStdDev = 2.0
		if v := os.Getenv("BOLL_MONITOR_STDDEV"); v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, fmt.Errorf("BOLL_MONITOR_STDDEV 无效: %s", v)
			}
			cfg.BollMonitorStdDev = f
		}

		cfg.BollMonitorCheckInterval = 1 * time.Minute
		if v := os.Getenv("BOLL_MONITOR_CHECK_INTERVAL"); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return nil, fmt.Errorf("BOLL_MONITOR_CHECK_INTERVAL 无效: %s（示例: 30s, 1m, 5m）", v)
			}
			cfg.BollMonitorCheckInterval = d
		}
	}

	return cfg, nil
}

// HasTelegram 检查是否配置了 Telegram 通知
func (c *Config) HasTelegram() bool {
	return c.TelegramBotToken != "" && c.TelegramChatID != ""
}

// EffectiveAPIKeys 返回当前模式下应使用的 API Key 和 Secret Key
func (c *Config) EffectiveAPIKeys() (apiKey, secretKey string) {
	if c.UseDemo {
		return c.BinanceDemoAPIKey, c.BinanceDemoSecretKey
	}
	return c.BinanceAPIKey, c.BinanceSecretKey
}
