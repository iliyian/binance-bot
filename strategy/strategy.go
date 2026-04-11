package strategy

import (
	"time"
)

// Kline 代表标准的 K 线数据 (OHLCV)
type Kline struct {
	Symbol    string
	Interval  string
	StartTime int64 // K 线起始时间戳 (毫秒)
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
}

// ActionType 代表交易方向
type ActionType string

const (
	Buy  ActionType = "BUY"
	Sell ActionType = "SELL"
	Hold ActionType = "HOLD"
)

// Action 代表策略作出的交易决策
type Action struct {
	Type   ActionType
	Symbol string
	Amount float64 // 买入金额 (如 USDT) 或 卖出数量 (如 BTC)
}

// TradeResult 代表交易执行的结果反馈
type TradeResult struct {
	Symbol      string
	Action      ActionType
	Success     bool
	OrderID     int64
	Price       float64 // 实际成交均价
	QuoteAmount float64 // 实际花费/获得的计价资产金额 (如 USDT)
	BaseAmount  float64 // 实际获得/花费的基础资产数量 (如 BTC)
	Commission  float64 // 手续费
	ExecutedAt  time.Time
	Error       error
}

// Context 提供策略运行时的上下文数据
type Context interface {
	// 获取当前的引擎时间 (实盘为当前时间，回测为虚拟时间)
	GetTime() time.Time

	// 获取指定交易对的当前最新价
	GetPrice(symbol string) float64

	// 获取历史 K 线数据，返回切片按时间顺序排列 (最新的一根在最后)
	GetKline(symbol, interval string, limit int) []Kline

	// 获取账户指定资产的可用余额 (如 "USDT", "BTC")
	GetBalance(asset string) float64
}

// Strategy 是所有交易策略必须实现的接口
type Strategy interface {
	// Init 初始化策略，接收配置参数
	Init(config map[string]string) error

	// Tick 是引擎每次心跳时调用的评估函数，策略在此做出决策
	Tick(ctx Context) []Action

	// OnTradeResult 交易撮合/执行后的回调，策略据此更新内部状态 (例如 pool)
	OnTradeResult(result TradeResult)

	// Name 返回策略标识名
	Name() string
}
