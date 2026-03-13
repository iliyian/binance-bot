package scheduler

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
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

	// pool: 每个交易对累积的未使用金额
	pool   map[string]float64
	poolMu sync.Mutex
}

// New 创建调度器
func New(cfg *config.Config, client *binance.Client, notifier *telegram.Notifier) *Scheduler {
	s := &Scheduler{
		cfg:      cfg,
		client:   client,
		notifier: notifier,
		pool:     make(map[string]float64),
	}

	// 从配置中加载初始 pool 值
	for i, pair := range cfg.TradePairs {
		pair = strings.TrimSpace(pair)
		if i < len(cfg.PoolAmounts) && cfg.PoolAmounts[i] > 0 {
			s.pool[pair] = cfg.PoolAmounts[i]
			log.Printf("🏊 %s 初始 Pool: %.8f USDT", pair, cfg.PoolAmounts[i])
		}
	}

	return s
}

// GetPool 获取当前 pool 状态（线程安全）
func (s *Scheduler) GetPool() map[string]float64 {
	s.poolMu.Lock()
	defer s.poolMu.Unlock()

	result := make(map[string]float64, len(s.pool))
	for k, v := range s.pool {
		result[k] = v
	}
	return result
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

	// 先计算有效金额（配置金额 + pool），后续赎回和交易都使用此金额
	s.poolMu.Lock()
	effectiveAmounts := make([]string, len(s.cfg.TradePairs))
	poolInfos := make([]telegram.PoolInfo, len(s.cfg.TradePairs))

	for i, pair := range s.cfg.TradePairs {
		pair = strings.TrimSpace(pair)
		configAmount, _ := strconv.ParseFloat(strings.TrimSpace(s.cfg.TradeAmounts[i]), 64)
		poolVal := s.pool[pair]
		effective := configAmount + poolVal

		effectiveAmounts[i] = fmt.Sprintf("%.8f", effective)
		poolInfos[i] = telegram.PoolInfo{
			Pair:       pair,
			PoolBefore: poolVal,
			Intended:   effective,
		}

		log.Printf("🏊 %s Pool: %.8f USDT, 配置: %.2f USDT, 本次实际定投: %.8f USDT",
			pair, poolVal, configAmount, effective)
	}
	s.poolMu.Unlock()

	// 如果开启了自动理财互转，先检查余额并赎回（使用 pool 叠加后的有效金额）
	var redeemResults []*binance.EarnTransferResult
	if s.cfg.AutoEarn {
		log.Println("📥 检查是否需要从活期理财赎回 USDT...")
		redeemResults = s.client.AutoRedeemIfNeeded(ctx, s.cfg.TradePairs, effectiveAmounts)
	}

	// 执行所有交易
	results := s.client.ExecuteAllTrades(ctx, s.cfg.TradePairs, effectiveAmounts)

	// 更新 pool：差值 = 意图金额 - 实际花费
	s.poolMu.Lock()
	for i, result := range results {
		pair := strings.TrimSpace(s.cfg.TradePairs[i])
		if result.Error != nil {
			// 交易失败，pool 保持不变（保留之前累积的值）
			poolInfos[i].PoolAfter = poolInfos[i].PoolBefore
			poolInfos[i].Actual = 0
			log.Printf("🏊 %s 交易失败，Pool 保持不变: %.8f USDT", pair, poolInfos[i].PoolBefore)
		} else {
			actualSpent, _ := strconv.ParseFloat(result.QuoteAmount, 64)
			newPool := poolInfos[i].Intended - actualSpent
			if newPool < 0 {
				newPool = 0 // 防止负值
			}
			s.pool[pair] = newPool
			poolInfos[i].PoolAfter = newPool
			poolInfos[i].Actual = actualSpent
			log.Printf("🏊 %s 意图: %.8f, 实际花费: %.8f, 差额入 Pool: %.8f USDT",
				pair, poolInfos[i].Intended, actualSpent, newPool)
		}
	}
	s.poolMu.Unlock()

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
		s.notifier.SendTradeReport(results, balance, redeemResults, purchaseResults, poolInfos)
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
