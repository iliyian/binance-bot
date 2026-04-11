package engine

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	gobinance "github.com/adshao/go-binance/v2"
	"github.com/iliyian/binance-bot/binance"
	"github.com/iliyian/binance-bot/config"
	"github.com/iliyian/binance-bot/data"
	"github.com/iliyian/binance-bot/strategy"
	"github.com/iliyian/binance-bot/telegram"
)

// LiveEngine 负责在实盘环境下驱动策略并连接真实的币安接口
type LiveEngine struct {
	strategy strategy.Strategy
	ctx      *BaseContext
	db       *data.DB
	client   *binance.Client
	notifier *telegram.Notifier
	cfg      *config.Config

	wsDoneC chan struct{}
}

// NewLiveEngine 初始化实盘引擎
func NewLiveEngine(db *data.DB, strat strategy.Strategy, client *binance.Client, notifier *telegram.Notifier, cfg *config.Config) *LiveEngine {
	return &LiveEngine{
		strategy: strat,
		ctx:      NewBaseContext(db),
		db:       db,
		client:   client,
		notifier: notifier,
		cfg:      cfg,
		wsDoneC:  make(chan struct{}),
	}
}

// 供回填逻辑获取策略的交易对，在 dca-pool 中可以通过反射或者新加接口获取
// 这里的启动回填暂且放在 Start 外部或通过外部传入 pairs 处理

// Start 启动引擎的主心跳
func (e *LiveEngine) Start(pairs []string) error {
	log.Printf("🚀 启动实盘引擎 [%s]...", e.strategy.Name())

	// 1. 订阅 WebSocket 实时价格 (零延迟，零 API 消耗)
	e.subscribePrices(pairs)

	// 2. 数据断层自动回填 (1天的数据，保证 MA 日线等不中断)
	e.autoBackfill(pairs)

	// 3. 开启 1 秒主循环 Ticker
	go e.runTickerLoop()

	return nil
}

// Stop 停止引擎
func (e *LiveEngine) Stop() {
	if e.wsDoneC != nil {
		close(e.wsDoneC)
		e.wsDoneC = nil
	}
}

// autoBackfill 在启动时自动修补 K 线断层
func (e *LiveEngine) autoBackfill(pairs []string) {
	log.Println("🔄 开始检查历史 K 线断层...")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	for _, p := range pairs {
		// 回填最近 30 天的日线
		_ = e.db.Backfill(ctx, e.client, p, "1d", 30)
		// 如果策略需要 1 分钟，也可以回填
	}
}

func (e *LiveEngine) subscribePrices(pairs []string) {
	streams := make([]string, len(pairs))
	for i, p := range pairs {
		// 监听归集交易 (aggTrade) 延迟极低
		streams[i] = strings.ToLower(p) + "@aggTrade"
	}

	wsHandler := func(event *gobinance.WsAggTradeEvent) {
		price, _ := strconv.ParseFloat(event.Price, 64)
		e.ctx.SetPrice(event.Symbol, price)
	}

	errHandler := func(err error) {
		log.Printf("⚠️ WebSocket 订阅异常: %v", err)
	}

	doneC, stopC, err := gobinance.WsCombinedAggTradeServe(streams, wsHandler, errHandler)
	if err != nil {
		log.Printf("❌ WebSocket 订阅失败: %v (策略将仅依赖 REST/DB 数据)", err)
		return
	}

	go func() {
		<-e.wsDoneC
		stopC <- struct{}{}
		<-doneC
		log.Println("🛑 WebSocket 已断开")
	}()

	log.Printf("📡 WebSocket 已连接，正在实时监听 %d 个交易对价格", len(pairs))
}

func (e *LiveEngine) runTickerLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case t := <-ticker.C:
			e.ctx.CurrentTime = t

			// 呼叫策略 Tick
			actions := e.strategy.Tick(e.ctx)

			if len(actions) > 0 {
				go e.executeBulkTrades(actions)
			}

		case <-e.wsDoneC:
			log.Println("⏹ 引擎心跳停止")
			return
		}
	}
}

// executeBulkTrades 批量处理策略吐出的指令（目前主要为了兼容 dca-pool 的自动理财互转逻辑）
func (e *LiveEngine) executeBulkTrades(actions []strategy.Action) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var pairs []string
	var amounts []string
	var amountsFloat []float64

	for _, a := range actions {
		if a.Type == strategy.Buy {
			pairs = append(pairs, a.Symbol)
			amounts = append(amounts, fmt.Sprintf("%.8f", a.Amount))
			amountsFloat = append(amountsFloat, a.Amount)
		} else {
			log.Printf("⚠️ 暂时不支持非 BUY 动作的批量自动处理: %v", a)
		}
	}

	if len(pairs) == 0 {
		return
	}

	// 1. 如果开启了理财，优先计算是否需要赎回 USDT
	var redeemResults []*binance.EarnTransferResult
	if e.cfg.AutoEarn {
		log.Println("📥 检查是否需要从活期理财赎回 USDT...")
		redeemResults = e.client.AutoRedeemIfNeeded(ctx, pairs, amounts)
	}

	// 2. 批量发起交易 (调用原有的封装逻辑)
	results := e.client.ExecuteAllTrades(ctx, pairs, amounts)

	// 3. 将真实交易结果反馈回策略 (用于 Pool 等内部状态维护)
	poolInfos := make([]telegram.PoolInfo, len(pairs))

	for i, r := range results {
		// 生成给策略的标准 TradeResult
		sRes := strategy.TradeResult{
			Symbol:     pairs[i],
			Action:     strategy.Buy, // 当前仅支持 bulk buy
			Success:    r.Error == nil,
			OrderID:    r.OrderID,
			ExecutedAt: r.ExecutedAt,
			Error:      r.Error,
		}

		intendedAmt := amountsFloat[i]
		poolInfos[i].Pair = pairs[i]
		poolInfos[i].Intended = intendedAmt

		if r.Error == nil {
			sRes.Price, _ = strconv.ParseFloat(r.AvgPrice, 64)
			sRes.QuoteAmount, _ = strconv.ParseFloat(r.QuoteAmount, 64)
			sRes.BaseAmount, _ = strconv.ParseFloat(r.FilledQty, 64)
			
			poolInfos[i].Actual = sRes.QuoteAmount
		}

		// 先获取更新前的 Pool
		if dca, ok := e.strategy.(*strategy.DCAPoolStrategy); ok {
			poolInfos[i].PoolBefore = dca.GetPool()[pairs[i]]
		}

		// 反馈给策略，策略会更新自身状态 (如 dca-pool 的 pool 金额)
		e.strategy.OnTradeResult(sRes)

		// 获取更新后的 Pool
		if dca, ok := e.strategy.(*strategy.DCAPoolStrategy); ok {
			poolInfos[i].PoolAfter = dca.GetPool()[pairs[i]]
		}
	}

	// 4. 将获得的资产买入理财
	var purchaseResults []*binance.EarnTransferResult
	if e.cfg.AutoEarn {
		log.Println("📤 将交易资产存入活期理财...")
		purchaseResults = e.client.AutoPurchaseToSavings(ctx, results)
	}

	// 5. 余额查询和通知
	balance := ""
	if bal, err := e.client.GetAccountBalance(ctx); err == nil {
		balance = bal
	}

	if e.notifier != nil {
		e.notifier.SendTradeReport(results, balance, redeemResults, purchaseResults, poolInfos)
	}
}

// RunOnce 提供给单次手动执行命令
func (e *LiveEngine) RunOnce() {
	// 直接触发一次虚拟的时间前进，因为是单次执行，可以直接把时间定为比当前晚，强制触发
	// 这里通过强制设成一个极大值时间，让策略误以为到达了。
	// 但这会破坏内部 cron 解析。更好的方式是在 dca-pool 内部开一个 ForceTick 或者直接调用 executeBulkTrades
	log.Println("🚨 RunOnce 不再通过 Tick 触发，而是直接抓取策略配置。这是旧版兼容模式")
	
	if dca, ok := e.strategy.(*strategy.DCAPoolStrategy); ok {
		pairs := dca.GetPairs()
		amounts := dca.GetAmounts()
		
		var actions []strategy.Action
		for i, p := range pairs {
			actions = append(actions, strategy.Action{
				Type:   strategy.Buy,
				Symbol: p,
				Amount: amounts[i], // 这里忽略 pool，单次执行通常是直接买
			})
		}
		e.executeBulkTrades(actions)
	}
}
