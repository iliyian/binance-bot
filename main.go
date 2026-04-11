package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/iliyian/binance-bot/binance"
	"github.com/iliyian/binance-bot/config"
	"github.com/iliyian/binance-bot/data"
	"github.com/iliyian/binance-bot/engine"
	"github.com/iliyian/binance-bot/strategy"
	"github.com/iliyian/binance-bot/telegram"
)

func main() {
	// 命令行参数
	runOnce := flag.Bool("once", false, "立即执行一次定投（不启动定时任务）")

	// 回测与数据参数
	isBacktest := flag.Bool("backtest", false, "启动回测模式")
	isBackfill := flag.Bool("backfill", false, "自动拉取历史数据填补本地 SQLite")
	btInterval := flag.String("interval", "1d", "回测/回填的时间粒度 (如 1s, 1m, 1h, 1d)")
	btDays := flag.Int("days", 365, "回测/回填拉取过去多少天的数据")
	btInitial := flag.Float64("initial", 10000, "回测虚拟初始资金 (USDT)")

	flag.Parse()

	// 初始化日志
	setupLog()

	log.Println("🚀 币安自动量化交易机器人启动中...")

	// 加载配置
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("❌ 配置加载失败: %v", err)
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

	// 创建 SQLite 数据库
	if err := os.MkdirAll("data_store", 0755); err != nil {
		log.Fatalf("无法创建数据库目录: %v", err)
	}
	db, err := data.NewDB("data_store/market.db")
	if err != nil {
		log.Fatalf("❌ SQLite 数据库初始化失败: %v", err)
	}
	defer db.Close()
	
	// 清理过期数据
	go func() {
		if err := db.CleanOldData(); err != nil {
			log.Printf("清理过期数据失败: %v", err)
		}
	}()

	// 统一配置转化，传递给 Strategy
	stratConfig := map[string]string{
		"TRADE_PAIRS":   strings.Join(cfg.TradePairs, ","),
		"TRADE_AMOUNTS": strings.Join(cfg.TradeAmounts, ","),
		"CRON_SCHEDULE": cfg.CronSchedule,
		"TIMEZONE":      cfg.Timezone.String(),
	}
	var poolStrs []string
	for _, p := range cfg.PoolAmounts {
		poolStrs = append(poolStrs, fmt.Sprintf("%f", p))
	}
	stratConfig["POOL_AMOUNTS"] = strings.Join(poolStrs, ",")

	// 实例化默认策略: dca-pool
	var activeStrategy strategy.Strategy = strategy.NewDCAPoolStrategy()
	if err := activeStrategy.Init(stratConfig); err != nil {
		log.Fatalf("❌ 策略初始化失败: %v", err)
	}
	log.Printf("🧠 已加载策略: %s", activeStrategy.Name())

	// ================= 回填模式 (Backfill) =================
	if *isBackfill {
		log.Println("🛠 进入数据回填模式 (Backfill)...")
		client := binance.NewClient(cfg.BinanceAPIKey, cfg.BinanceSecretKey, cfg.UseDemo, cfg.UseTestnet, cfg.BinanceBaseURL)
		ctx := context.Background()
		for _, pair := range cfg.TradePairs {
			if err := db.Backfill(ctx, client, pair, *btInterval, *btDays); err != nil {
				log.Printf("❌ 回填 %s 失败: %v", pair, err)
			}
		}
		log.Println("✅ 数据回填全部完成，退出程序。")
		return
	}

	// ================= 回测模式 (Backtest) =================
	if *isBacktest {
		log.Println("🔬 进入离线回测模式 (Backtest)...")
		
		now := time.Now()
		startTime := now.Add(time.Duration(-*btDays*24) * time.Hour)
		
		btEngine := engine.NewBacktestEngine(db, activeStrategy, *btInterval, startTime, now, *btInitial)
		btEngine.Run(cfg.TradePairs)
		return
	}

	// ================= 实盘模式 (Live) =================
	log.Printf("📋 交易对: %v", cfg.TradePairs)
	log.Printf("💰 投入金额: %v USDT", cfg.TradeAmounts)
	log.Printf("⏰ 定时规则: %s", cfg.CronSchedule)
	if cfg.AutoEarn {
		log.Println("🏦 自动活期理财互转已启用")
	}

	client := binance.NewClient(cfg.BinanceAPIKey, cfg.BinanceSecretKey, cfg.UseDemo, cfg.UseTestnet, cfg.BinanceBaseURL)
	if cfg.LogLevel == "debug" {
		client.SetDebug(true)
		log.Println("🐛 调试模式已启用")
	}

	var notifier *telegram.Notifier
	var bot *telegram.Bot
	if cfg.HasTelegram() {
		notifier = telegram.NewNotifier(cfg.TelegramBotToken, cfg.TelegramChatID)
		bot = telegram.NewBot(cfg.TelegramBotToken, cfg.TelegramChatID, client)
		log.Println("📨 Telegram 通知已启用")
	} else {
		log.Println("⚠️ Telegram 通知未配置，将仅输出日志")
	}

	liveEngine := engine.NewLiveEngine(db, activeStrategy, client, notifier, cfg)

	if bot != nil {
		if dca, ok := activeStrategy.(*strategy.DCAPoolStrategy); ok {
			bot.SetPoolGetter(dca.GetPool)
		}
	}

	if *runOnce {
		log.Println("🔔 单次执行模式")
		liveEngine.RunOnce()
		log.Println("✅ 执行完毕")
		return
	}

	if bot != nil {
		bot.Start()
	}

	if err := liveEngine.Start(cfg.TradePairs); err != nil {
		log.Fatalf("❌ 引擎启动失败: %v", err)
	}

	if notifier != nil {
		notifier.SendStartupNotice(cfg.TradePairs, cfg.TradeAmounts, cfg.CronSchedule, cfg.PoolAmounts)
	}

	log.Println("✅ 机器人运行中，按 Ctrl+C 退出")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	log.Printf("📛 收到信号 %v，正在关闭...", sig)
	liveEngine.Stop()
	if bot != nil {
		bot.Stop()
	}
	log.Println("👋 已安全退出")
}

func setupLog() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[binance-bot] ")

	logDir := "logs"
	logFile := filepath.Join(logDir, "app.log")

	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("⚠️ 无法创建日志目录: %v", err)
		return
	}

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("⚠️ 无法打开日志文件: %v", err)
		return
	}

	multiWriter := io.MultiWriter(os.Stdout, f)
	log.SetOutput(multiWriter)
}
