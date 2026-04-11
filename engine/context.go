package engine

import (
	"sync"
	"time"

	"github.com/iliyian/binance-bot/data"
	"github.com/iliyian/binance-bot/strategy"
)

// BaseContext 实现了 strategy.Context 接口
// 它不仅被回测引擎使用，也是实盘引擎的数据总线
type BaseContext struct {
	CurrentTime time.Time
	DB          *data.DB

	// CurrentPrices 存放最新的价格
	// 回测时：等于当前历史切片的 Close 价格
	// 实盘时：由 WebSocket 实时更新
	CurrentPrices map[string]float64
	pricesMu      sync.RWMutex

	// Balances 账户虚拟/真实余额
	Balances map[string]float64
}

// NewBaseContext 构造 Context
func NewBaseContext(db *data.DB) *BaseContext {
	return &BaseContext{
		DB:            db,
		CurrentPrices: make(map[string]float64),
		Balances:      make(map[string]float64),
	}
}

// GetTime 返回当前上下文的时间
func (c *BaseContext) GetTime() time.Time {
	return c.CurrentTime
}

// SetPrice 设置当前上下文价格（并发安全）
func (c *BaseContext) SetPrice(symbol string, price float64) {
	c.pricesMu.Lock()
	defer c.pricesMu.Unlock()
	c.CurrentPrices[symbol] = price
}

// GetPrice 返回当前上下文价格（并发安全）
func (c *BaseContext) GetPrice(symbol string) float64 {
	c.pricesMu.RLock()
	defer c.pricesMu.RUnlock()
	return c.CurrentPrices[symbol]
}

// GetKline 返回防未来函数的历史 K 线
func (c *BaseContext) GetKline(symbol, interval string, limit int) []strategy.Kline {
	if c.DB == nil {
		return nil
	}
	// unixMilli 限制只查过去的数据
	klines, err := c.DB.GetKlines(symbol, interval, limit, c.CurrentTime.UnixMilli())
	if err != nil {
		return nil
	}
	return klines
}

// GetBalance 返回指定资产的余额
func (c *BaseContext) GetBalance(asset string) float64 {
	return c.Balances[asset]
}
