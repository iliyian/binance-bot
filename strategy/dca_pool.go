package strategy

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// DCAPoolStrategy 实现了带资金池的定时定投策略
type DCAPoolStrategy struct {
	pairs        []string
	amounts      []float64
	cronSchedule string
	timezone     *time.Location

	pool   map[string]float64
	poolMu sync.Mutex

	schedule    cron.Schedule
	nextRunTime time.Time
}

// NewDCAPoolStrategy 创建定投策略实例
func NewDCAPoolStrategy() *DCAPoolStrategy {
	return &DCAPoolStrategy{
		pool: make(map[string]float64),
	}
}

// Name 返回策略名称
func (s *DCAPoolStrategy) Name() string {
	return "dca-pool"
}

// Init 初始化策略参数
func (s *DCAPoolStrategy) Init(config map[string]string) error {
	pairsStr := config["TRADE_PAIRS"]
	if pairsStr == "" {
		return fmt.Errorf("TRADE_PAIRS 未配置")
	}
	s.pairs = strings.Split(pairsStr, ",")
	for i := range s.pairs {
		s.pairs[i] = strings.TrimSpace(s.pairs[i])
	}

	amountsStr := config["TRADE_AMOUNTS"]
	if amountsStr == "" {
		return fmt.Errorf("TRADE_AMOUNTS 未配置")
	}
	amountParts := strings.Split(amountsStr, ",")
	if len(amountParts) != len(s.pairs) {
		return fmt.Errorf("TRADE_PAIRS 和 TRADE_AMOUNTS 数量不一致")
	}

	for _, a := range amountParts {
		v, err := strconv.ParseFloat(strings.TrimSpace(a), 64)
		if err != nil {
			return fmt.Errorf("无效的 TRADE_AMOUNTS 值: %s", a)
		}
		s.amounts = append(s.amounts, v)
	}

	s.cronSchedule = config["CRON_SCHEDULE"]
	if s.cronSchedule == "" {
		s.cronSchedule = "0 0 9 * * *"
	}

	tzStr := config["TIMEZONE"]
	if tzStr == "" {
		tzStr = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(tzStr)
	if err != nil {
		return fmt.Errorf("无效的时区: %s", tzStr)
	}
	s.timezone = loc

	// 解析初始 pool
	poolStr := config["POOL_AMOUNTS"]
	if poolStr != "" {
		poolParts := strings.Split(poolStr, ",")
		for i, p := range poolParts {
			if i >= len(s.pairs) {
				break
			}
			val, _ := strconv.ParseFloat(strings.TrimSpace(p), 64)
			if val > 0 {
				s.pool[s.pairs[i]] = val
				log.Printf("🏊 [dca-pool] %s 初始 Pool: %.8f USDT", s.pairs[i], val)
			}
		}
	}

	// 解析 cron 以计算下次执行时间
	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	sch, err := parser.Parse(s.cronSchedule)
	if err != nil {
		return fmt.Errorf("解析 cron 失败: %w", err)
	}
	s.schedule = sch

	// 这里初始化一个极早的时间，确保在第一次 Tick() 时能正常算出真实的下一次
	s.nextRunTime = time.Time{}

	return nil
}

// Tick 按时钟周期判断是否触发定投
func (s *DCAPoolStrategy) Tick(ctx Context) []Action {
	now := ctx.GetTime().In(s.timezone)

	// 如果这是首次 Tick()，或者现在的时间恰好越过了预计时间
	if s.nextRunTime.IsZero() {
		s.nextRunTime = s.schedule.Next(now)
		log.Printf("⏰ [dca-pool] 已启动，下次定投计划时间: %s", s.nextRunTime.Format(time.DateTime))
		return nil
	}

	if now.After(s.nextRunTime) || now.Equal(s.nextRunTime) {
		// 生成决策后，立即计算随后的下一次时间
		s.nextRunTime = s.schedule.Next(now)
		log.Printf("⏰ [dca-pool] 定投时间到！当前时间: %s，更新下次计划时间为: %s", now.Format(time.DateTime), s.nextRunTime.Format(time.DateTime))

		var actions []Action
		s.poolMu.Lock()
		for i, pair := range s.pairs {
			baseAmt := s.amounts[i]
			poolVal := s.pool[pair]
			intended := baseAmt + poolVal

			actions = append(actions, Action{
				Type:   Buy,
				Symbol: pair,
				Amount: intended,
			})
			log.Printf("📊 [dca-pool] 生成信号 %s: BUY %.8f (基础 %.2f + 遗留池 %.8f)", pair, intended, baseAmt, poolVal)
		}
		s.poolMu.Unlock()
		return actions
	}

	return nil
}

// OnTradeResult 根据执行结果更新资金池
func (s *DCAPoolStrategy) OnTradeResult(result TradeResult) {
	if result.Action != Buy {
		return
	}

	s.poolMu.Lock()
	defer s.poolMu.Unlock()

	var baseAmt float64
	for i, p := range s.pairs {
		if p == result.Symbol {
			baseAmt = s.amounts[i]
			break
		}
	}

	if baseAmt == 0 {
		return // 该交易对不在策略内
	}

	oldPool := s.pool[result.Symbol]
	intended := baseAmt + oldPool

	if !result.Success {
		// 原代码逻辑：交易失败，pool 保持不变（不追加本次金额进 pool，直接当没发生过）
		log.Printf("❌🏊 [dca-pool] %s 交易失败，Pool 保持原有余额不变: %.8f USDT", result.Symbol, oldPool)
	} else {
		actualSpent := result.QuoteAmount
		newPool := intended - actualSpent
		s.pool[result.Symbol] = newPool
		log.Printf("✅🏊 [dca-pool] %s 意图花费: %.8f, 实际花费: %.8f, 剩余入 Pool: %.8f USDT",
			result.Symbol, intended, actualSpent, newPool)
	}
}

// GetPool 提供给 Telegram 模块展示当前池子的接口
func (s *DCAPoolStrategy) GetPool() map[string]float64 {
	s.poolMu.Lock()
	defer s.poolMu.Unlock()
	res := make(map[string]float64)
	for k, v := range s.pool {
		res[k] = v
	}
	return res
}

// GetPairs 获取策略配置的交易对列表
func (s *DCAPoolStrategy) GetPairs() []string {
	return s.pairs
}

// GetAmounts 获取策略配置的基准金额列表
func (s *DCAPoolStrategy) GetAmounts() []float64 {
	return s.amounts
}
