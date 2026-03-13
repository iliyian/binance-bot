package scheduler

import (
	"context"
	"log"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/iliyian/binance-bot/binance"
	"github.com/iliyian/binance-bot/config"
	"github.com/iliyian/binance-bot/telegram"
)

// Scheduler 定时调度器
type Scheduler struct {
	cfg      *config.Config
	client   *binance.Client
	notifier *telegram.Notifier
	cron     *cron.Cron
}

// New 创建调度器
func New(cfg *config.Config, client *binance.Client, notifier *telegram.Notifier) *Scheduler {
	return &Scheduler{
		cfg:      cfg,
		client:   client,
		notifier: notifier,
	}
}

// Start 启动定时任务
func (s *Scheduler) Start() error {
	// 创建带时区的 cron 调度器，支持秒级精度
	s.cron = cron.New(
		cron.WithLocation(s.cfg.Timezone),
		cron.WithSeconds(),
		cron.WithChain(cron.Recover(cron.DefaultLogger)),
	)

	_, err := s.cron.AddFunc(s.cfg.CronSchedule, s.executeTrades)
	if err != nil {
		return err
	}

	s.cron.Start()

	// 计算下次执行时间
	entries := s.cron.Entries()
	if len(entries) > 0 {
		next := entries[0].Next.In(s.cfg.Timezone)
		log.Printf("⏰ 定时任务已启动，下次执行时间: %s", next.Format("2006-01-02 15:04:05"))
	}

	return nil
}

// Stop 停止定时任务
func (s *Scheduler) Stop() {
	if s.cron != nil {
		ctx := s.cron.Stop()
		<-ctx.Done()
		log.Println("⏹ 定时任务已停止")
	}
}

// executeTrades 执行定投交易
func (s *Scheduler) executeTrades() {
	log.Println("🔔 定投任务触发，开始执行...")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// 如果开启了自动理财互转，先检查余额并赎回
	var redeemResults []*binance.EarnTransferResult
	if s.cfg.AutoEarn {
		log.Println("📥 检查是否需要从活期理财赎回 USDT...")
		redeemResults = s.client.AutoRedeemIfNeeded(ctx, s.cfg.TradePairs, s.cfg.TradeAmounts)
	}

	// 执行所有交易
	results := s.client.ExecuteAllTrades(ctx, s.cfg.TradePairs, s.cfg.TradeAmounts)

	// 如果开启了自动理财互转，将交易获得的资产存入活期理财
	var purchaseResults []*binance.EarnTransferResult
	if s.cfg.AutoEarn {
		log.Println("📤 将交易资产存入活期理财...")
		purchaseResults = s.client.AutoPurchaseToSavings(ctx, results)
	}

	// 查询余额
	balance := ""
	if bal, err := s.client.GetAccountBalance(ctx); err == nil {
		balance = bal
	} else {
		log.Printf("⚠️ 查询余额失败: %v", err)
	}

	// 发送 Telegram 通知
	if s.notifier != nil {
		s.notifier.SendTradeReport(results, balance, redeemResults, purchaseResults)
	}

	// 打印下次执行时间
	if s.cron != nil {
		entries := s.cron.Entries()
		if len(entries) > 0 {
			next := entries[0].Next.In(s.cfg.Timezone)
			log.Printf("⏰ 下次执行时间: %s", next.Format("2006-01-02 15:04:05"))
		}
	}
}

// RunOnce 手动执行一次（用于测试）
func (s *Scheduler) RunOnce() {
	s.executeTrades()
}
