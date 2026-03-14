package main

import (
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/iliyian/binance-bot/binance"
	"github.com/iliyian/binance-bot/config"
	"github.com/iliyian/binance-bot/scheduler"
	"github.com/iliyian/binance-bot/telegram"
)

func main() {
	// 命令行参数
	runOnce := flag.Bool("once", false, "立即执行一次定投（不启动定时任务）")
	flag.Parse()

	// 初始化日志
	setupLog()

	log.Println("🚀 币安自动定投机器人启动中...")

	// 加载配置
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("❌ 配置加载失败: %v", err)
	}

	log.Printf("📋 交易对: %v", cfg.TradePairs)
	log.Printf("💰 投入金额: %v USDT", cfg.TradeAmounts)
	log.Printf("🏊 初始 Pool: %v USDT", cfg.PoolAmounts)
	log.Printf("⏰ 定时规则: %s", cfg.CronSchedule)
	log.Printf("🌍 时区: %s", cfg.Timezone.String())
	if cfg.AutoEarn {
		log.Println("🏦 自动活期理财互转已启用")
	}
	if cfg.UseDemo {
		log.Println("🧪 使用模拟交易模式 (demo.binance.com)")
	}
	if cfg.UseTestnet {
		log.Println("⚠️ 使用测试网模式 (testnet.binance.vision)")
	}

	// 打印代理信息
	if proxy := os.Getenv("HTTPS_PROXY"); proxy != "" {
		log.Printf("🌐 HTTPS 代理: %s", proxy)
	} else if proxy := os.Getenv("https_proxy"); proxy != "" {
		log.Printf("🌐 HTTPS 代理: %s", proxy)
	} else if proxy := os.Getenv("HTTP_PROXY"); proxy != "" {
		log.Printf("🌐 HTTP 代理: %s", proxy)
	} else if proxy := os.Getenv("http_proxy"); proxy != "" {
		log.Printf("🌐 HTTP 代理: %s", proxy)
	}

	// 创建币安客户端
	client := binance.NewClient(cfg.BinanceAPIKey, cfg.BinanceSecretKey, cfg.UseDemo, cfg.UseTestnet, cfg.BinanceBaseURL)
	if cfg.LogLevel == "debug" {
		client.SetDebug(true)
		log.Println("🐛 调试模式已启用")
	}

	// 创建 Telegram 通知器
	var notifier *telegram.Notifier
	var bot *telegram.Bot
	if cfg.HasTelegram() {
		notifier = telegram.NewNotifier(cfg.TelegramBotToken, cfg.TelegramChatID)
		bot = telegram.NewBot(cfg.TelegramBotToken, cfg.TelegramChatID, client)
		log.Println("📨 Telegram 通知已启用")
	} else {
		log.Println("⚠️ Telegram 通知未配置，将仅输出日志")
	}

	// 创建调度器
	sched := scheduler.New(cfg, client, notifier)

	// 将 pool 查询回调连接到 Telegram Bot
	if bot != nil {
		bot.SetPoolGetter(sched.GetPool)
	}

	// 如果是单次执行模式
	if *runOnce {
		log.Println("🔔 单次执行模式")
		sched.RunOnce()
		log.Println("✅ 执行完毕")
		return
	}

	// 启动 Telegram Bot 命令监听
	if bot != nil {
		bot.Start()
	}

	// 启动定时任务
	if err := sched.Start(); err != nil {
		log.Fatalf("❌ 定时任务启动失败: %v", err)
	}

	// 发送启动通知
	if notifier != nil {
		notifier.SendStartupNotice(cfg.TradePairs, cfg.TradeAmounts, cfg.CronSchedule, cfg.PoolAmounts)
	}

	log.Println("✅ 机器人运行中，按 Ctrl+C 退出")

	// 优雅退出
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	log.Printf("📛 收到信号 %v，正在关闭...", sig)
	sched.Stop()
	if bot != nil {
		bot.Stop()
	}
	log.Println("👋 已安全退出")
}

func setupLog() {
	// 设置日志格式
	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[binance-bot] ")

	logDir := "logs"
	logFile := filepath.Join(logDir, "app.log")

	// 确保日志目录存在
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("⚠️ 无法创建日志目录: %v", err)
		return
	}

	// 打开日志文件（追加模式）
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Printf("⚠️ 无法打开日志文件: %v", err)
		return
	}

	// 同时输出到控制台和文件
	multiWriter := io.MultiWriter(os.Stdout, f)
	log.SetOutput(multiWriter)
}
