package engine

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/iliyian/binance-bot/data"
	"github.com/iliyian/binance-bot/strategy"
)

// BacktestEngine 提供全量历史数据离线回测功能
type BacktestEngine struct {
	strategy strategy.Strategy
	ctx      *BaseContext
	db       *data.DB

	interval string // 引擎推进的时间粒度
	start    time.Time
	end      time.Time

	trades []strategy.TradeResult

	// 初始投入统计
	initialUSDT float64

	orderCounter int64
}

// NewBacktestEngine 创建回测引擎
func NewBacktestEngine(db *data.DB, strat strategy.Strategy, interval string, start, end time.Time, initialBalance float64) *BacktestEngine {
	ctx := NewBaseContext(db)
	ctx.Balances["USDT"] = initialBalance // 虚拟启动资金

	return &BacktestEngine{
		strategy:    strat,
		ctx:         ctx,
		db:          db,
		interval:    interval,
		start:       start,
		end:         end,
		initialUSDT: initialBalance,
		trades:      make([]strategy.TradeResult, 0),
	}
}

func getDuration(interval string) time.Duration {
	switch interval {
	case "1s":
		return time.Second
	case "1m":
		return time.Minute
	case "1h":
		return time.Hour
	case "1d":
		return 24 * time.Hour
	default:
		return time.Minute
	}
}

// Run 执行回测循环
func (e *BacktestEngine) Run(pairs []string) {
	log.Printf("🚀 开始回测 [%s] | 步长: %s | 资金: %.2f | 区间: %s -> %s",
		e.strategy.Name(), e.interval, e.initialUSDT, e.start.Format(time.DateTime), e.end.Format(time.DateTime))

	step := getDuration(e.interval)
	tickCount := 0

	// 时间从 start 一步步向 end 推进
	for t := e.start; t.Before(e.end) || t.Equal(e.end); t = t.Add(step) {
		e.ctx.CurrentTime = t
		tickCount++

		// 1. 每一步，更新所有标的物的最新价格（防止策略只拿价不拿 K 线的情况）
		for _, pair := range pairs {
			// 取数据库中对应虚拟时间之前的最近一根 K 线的 Open，防止未来函数泄露
			klines, err := e.db.GetKlines(pair, e.interval, 1, t.UnixMilli())
			if err == nil && len(klines) > 0 {
				e.ctx.SetPrice(pair, klines[0].Open)
			}
		}

		// 2. 调用策略 Tick
		actions := e.strategy.Tick(e.ctx)

		// 3. 模拟撮合订单
		for _, action := range actions {
			e.simulateExecute(action)
		}
	}

	log.Printf("✅ 回测完成，共推进 %d 个 Tick。", tickCount)
	e.printReport(pairs)
}

// simulateExecute 进行简易的市价单撮合模拟，并扣除手续费
func (e *BacktestEngine) simulateExecute(action strategy.Action) {
	price := e.ctx.GetPrice(action.Symbol)
	if price <= 0 {
		log.Printf("⚠️ 警告：跳过执行。无法获取 %s 的有效价格", action.Symbol)
		
		// 向策略反馈失败
		e.strategy.OnTradeResult(strategy.TradeResult{
			Symbol: action.Symbol, Action: action.Type, Success: false,
		})
		return
	}

	feeRate := 0.001 // 默认 0.1% 现货手续费

	e.orderCounter++
	res := strategy.TradeResult{
		Symbol:     action.Symbol,
		Action:     action.Type,
		Success:    true,
		OrderID:    e.orderCounter,
		Price:      price,
		ExecutedAt: e.ctx.CurrentTime,
	}

	// 动态拆分基础资产和计价资产
	baseCurrency := action.Symbol
	quoteCurrency := "USDT" // 默认
	knownQuotes := []string{"USDT", "USDC", "FDUSD", "BUSD", "TUSD", "BTC", "ETH", "BNB"}
	for _, q := range knownQuotes {
		if strings.HasSuffix(action.Symbol, q) {
			baseCurrency = strings.TrimSuffix(action.Symbol, q)
			quoteCurrency = q
			break
		}
	}

	if action.Type == strategy.Buy {
		if e.ctx.Balances[quoteCurrency] < action.Amount {
			// 由于 DCA 的资金可能是外置输入的增量，这里仅仅只是记录透支或者允许。
			// 为了方便报告期末收益率，我们允许计价货币变成负数（视为累计投入金额）。
		}
		
		res.QuoteAmount = action.Amount
		rawBase := action.Amount / price
		res.Commission = rawBase * feeRate
		res.BaseAmount = rawBase - res.Commission

		e.ctx.Balances[quoteCurrency] -= res.QuoteAmount
		e.ctx.Balances[baseCurrency] += res.BaseAmount

	} else if action.Type == strategy.Sell {
		res.BaseAmount = action.Amount
		rawQuote := action.Amount * price
		res.Commission = rawQuote * feeRate
		res.QuoteAmount = rawQuote - res.Commission

		e.ctx.Balances[quoteCurrency] += res.QuoteAmount
		e.ctx.Balances[baseCurrency] -= res.BaseAmount
	}

	e.trades = append(e.trades, res)
	// 将结果反馈给策略
	e.strategy.OnTradeResult(res)
}

// printReport 打印精美的期末回测报告
func (e *BacktestEngine) printReport(pairs []string) {
	fmt.Println("\n================= 回测报告 ==================")
	fmt.Printf("策略名称: %s\n", e.strategy.Name())
	fmt.Printf("回测区间: %s 到 %s\n", e.start.Format(time.DateOnly), e.end.Format(time.DateOnly))
	fmt.Printf("总交易笔数: %d 笔\n", len(e.trades))
	fmt.Println("-------------------------------------------")

	// 为了报告，假设使用启动时的主要计价货币
	mainQuote := "USDT" 
	
	finalValue := e.ctx.Balances[mainQuote]
	totalSpent := e.initialUSDT - e.ctx.Balances[mainQuote]
	
	fmt.Printf("总计入金 (投入 %s): %.2f\n", mainQuote, totalSpent)
	fmt.Printf("期末账户 %s 余额: %.2f\n", mainQuote, e.ctx.Balances[mainQuote])

	fmt.Println("期末各币种持仓及现值:")
	for _, pair := range pairs {
		baseCurrency := pair
		quoteCurrency := mainQuote
		for _, q := range []string{"USDT", "USDC", "FDUSD", "BUSD", "TUSD", "BTC", "ETH", "BNB"} {
			if strings.HasSuffix(pair, q) {
				baseCurrency = strings.TrimSuffix(pair, q)
				quoteCurrency = q
				break
			}
		}

		balance := e.ctx.Balances[baseCurrency]
		
		// 使用期末最后一笔已知价格估值
		lastPrice := e.ctx.GetPrice(pair)
		value := balance * lastPrice
		
		if quoteCurrency == mainQuote {
			finalValue += value
		}
		
		fmt.Printf("  - %s: 数量 %.6f | 尾盘现价: %.2f | 估值: %.2f %s\n", 
			baseCurrency, balance, lastPrice, value, quoteCurrency)
	}
	
	fmt.Println("-------------------------------------------")
	fmt.Printf("期末资产总估值: %.2f %s\n", finalValue, mainQuote)
	
	// 计算收益率: (现值 - 总投入) / 总投入
	if totalSpent > 0 {
		profit := finalValue - e.initialUSDT
		roi := (profit / totalSpent) * 100
		fmt.Printf("累计总盈亏: %.2f %s\n", profit, mainQuote)
		fmt.Printf("绝对收益率 (ROI): %.2f%%\n", roi)
	}

	fmt.Println("===========================================\n")
}
