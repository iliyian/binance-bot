package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

// Config 应用配置结构
type Config struct {
	// Binance API
	BinanceAPIKey    string
	BinanceSecretKey string

	// 定投配置
	TradePairs   []string      // 交易对列表
	TradeAmounts []string      // 对应金额列表 (字符串保留精度)
	PoolAmounts  []float64     // 每个交易对的初始 pool 值 (用于重启恢复)
	CronSchedule string        // cron 表达式
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

	mu sync.Mutex // 保护 TradeAmounts 并发写入
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
	if cfg.AutoEarn && (cfg.UseDemo || cfg.UseTestnet) {
		return nil, fmt.Errorf("AUTO_EARN 不能与 USE_DEMO 或 USE_TESTNET 同时启用（模拟/测试环境不支持理财 API）")
	}

	return cfg, nil
}

// HasTelegram 检查是否配置了 Telegram 通知
func (c *Config) HasTelegram() bool {
	return c.TelegramBotToken != "" && c.TelegramChatID != ""
}

// UpdateTradeAmount 更新指定交易对的定投金额（同时保存到 .env 文件）
func (c *Config) UpdateTradeAmount(pair string, amount string) error {
	// 验证金额
	val, err := strconv.ParseFloat(strings.TrimSpace(amount), 64)
	if err != nil || val <= 0 {
		return fmt.Errorf("无效金额: %s", amount)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// 查找交易对索引
	idx := -1
	for i, p := range c.TradePairs {
		if strings.EqualFold(strings.TrimSpace(p), strings.TrimSpace(pair)) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("交易对 %s 未在配置中找到", pair)
	}

	// 更新内存中的值
	c.TradeAmounts[idx] = strings.TrimSpace(amount)

	// 保存到 .env 文件
	return c.saveToEnvFile()
}

// saveToEnvFile 将当前 TRADE_AMOUNTS 写回 .env 文件，保留原有注释和变量顺序（调用方须持有 c.mu）
func (c *Config) saveToEnvFile() error {
	const envFile = ".env"
	newValue := strings.Join(c.TradeAmounts, ",")
	key := "TRADE_AMOUNTS"

	// 读取原始文件内容
	data, err := os.ReadFile(envFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("读取 .env 失败: %w", err)
	}

	var lines []string
	found := false

	if len(data) > 0 {
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := scanner.Text()
			// 匹配 KEY=... 行（跳过注释行和空行）
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "#") && strings.HasPrefix(trimmed, key+"=") {
				lines = append(lines, key+"="+newValue)
				found = true
			} else {
				lines = append(lines, line)
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("解析 .env 失败: %w", err)
		}
	}

	// 若文件中原本没有该键，追加到末尾
	if !found {
		lines = append(lines, key+"="+newValue)
	}

	content := strings.Join(lines, "\n")
	// 保留文件末尾换行符（如果原文件有）
	if len(data) > 0 && data[len(data)-1] == '\n' {
		content += "\n"
	}

	return os.WriteFile(envFile, []byte(content), 0644)
}
