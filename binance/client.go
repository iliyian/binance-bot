package binance

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	gobinance "github.com/adshao/go-binance/v2"
)

const (
	// DemoBaseURL 币安模拟交易 REST API 地址 (demo.binance.com)
	DemoBaseURL = "https://demo-api.binance.com"
)

// TradeResult 单笔交易结果
type TradeResult struct {
	Symbol          string
	OrderID         int64
	QuoteAmount     string // 花费的报价货币 (如 USDT)
	FilledQty       string // 获得的基础货币数量
	AvgPrice        string // 成交均价
	Commission      string // 总手续费
	CommissionAsset string // 手续费资产类型
	Status          string
	ExecutedAt      time.Time
	Error           error
}

// Client 币安交易客户端
type Client struct {
	client     *gobinance.Client
	httpClient *http.Client
	apiKey     string
	secretKey  string
	debug      bool
}

// SetDebug 设置调试模式
func (c *Client) SetDebug(debug bool) {
	c.debug = debug
}

// debugLog 仅在调试模式下输出日志
func (c *Client) debugLog(format string, v ...interface{}) {
	if c.debug {
		log.Printf(format, v...)
	}
}

// NewClient 创建币安客户端
// 优先级: baseURL > useDemo > useTestnet > 生产环境
func NewClient(apiKey, secretKey string, useDemo, useTestnet bool, baseURL string) *Client {
	if useTestnet {
		gobinance.UseTestnet = true
	}
	client := gobinance.NewClient(apiKey, secretKey)

	// 自定义 Base URL 优先级最高
	if baseURL != "" {
		client.BaseURL = baseURL
		log.Printf("🔗 使用自定义 API 地址: %s", baseURL)
	} else if useDemo {
		// 使用 demo.binance.com 模拟交易
		client.BaseURL = DemoBaseURL
		log.Printf("🔗 使用模拟交易 API: %s", DemoBaseURL)
	}

	c := &Client{
		client:     client,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		apiKey:     apiKey,
		secretKey:  secretKey,
	}

	// 启动时同步服务器时间
	if err := c.SyncServerTime(); err != nil {
		log.Printf("⚠️ 初始时间同步失败: %v（将在交易前重试）", err)
	}

	return c
}

// SyncServerTime 同步币安服务器时间，使用库内置的 SetServerTimeService
// 自动计算并设置 client.TimeOffset
func (c *Client) SyncServerTime() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	timeOffset, err := c.client.NewSetServerTimeService().Do(ctx)
	if err != nil {
		return fmt.Errorf("时间同步失败: %w", err)
	}

	log.Printf("🕐 时间同步完成，偏移量: %dms", timeOffset)
	return nil
}

// ExecuteMarketBuy 执行市价买入
// symbol: 交易对 (如 BTCUSDT)
// quoteAmount: 使用的报价货币金额 (如 10 表示 10 USDT)
func (c *Client) ExecuteMarketBuy(ctx context.Context, symbol, quoteAmount string) *TradeResult {
	result := &TradeResult{
		Symbol:     symbol,
		ExecutedAt: time.Now(),
	}

	// 使用 quoteOrderQty 按金额市价买入
	order, err := c.client.NewCreateOrderService().
		Symbol(symbol).
		Side(gobinance.SideTypeBuy).
		Type(gobinance.OrderTypeMarket).
		QuoteOrderQty(quoteAmount).
		Do(ctx)

	if err != nil {
		result.Error = fmt.Errorf("下单失败 [%s]: %w", symbol, err)
		result.Status = "FAILED"
		return result
	}

	result.OrderID = order.OrderID
	result.Status = string(order.Status)
	result.FilledQty = order.ExecutedQuantity
	result.QuoteAmount = order.CummulativeQuoteQuantity

	// 计算成交均价
	if filledQty, err := strconv.ParseFloat(order.ExecutedQuantity, 64); err == nil && filledQty > 0 {
		if quoteQty, err := strconv.ParseFloat(order.CummulativeQuoteQuantity, 64); err == nil {
			result.AvgPrice = fmt.Sprintf("%.8f", quoteQty/filledQty)
		}
	}

	// 汇总手续费（从 Fills 中提取）
	if len(order.Fills) > 0 {
		commissionMap := make(map[string]float64) // 按资产类型汇总
		for _, fill := range order.Fills {
			comm, _ := strconv.ParseFloat(fill.Commission, 64)
			commissionMap[fill.CommissionAsset] += comm
		}
		// 构建手续费字符串
		var commParts []string
		for asset, amount := range commissionMap {
			commParts = append(commParts, fmt.Sprintf("%s %s", strconv.FormatFloat(amount, 'f', -1, 64), asset))
		}
		result.Commission = strings.Join(commParts, " + ")
		// 记录主要手续费资产
		if len(commissionMap) == 1 {
			for asset := range commissionMap {
				result.CommissionAsset = asset
			}
		}
	}

	return result
}

// ExecuteAllTrades 执行所有定投交易
func (c *Client) ExecuteAllTrades(ctx context.Context, pairs []string, amounts []string) []*TradeResult {
	// 每次交易前重新同步服务器时间
	if err := c.SyncServerTime(); err != nil {
		log.Printf("⚠️ 交易前时间同步失败: %v", err)
	}

	// 预检：查询 USDT 可用余额
	totalRequired := 0.0
	for _, amt := range amounts {
		v, _ := strconv.ParseFloat(strings.TrimSpace(amt), 64)
		totalRequired += v
	}
	if freeBalance, err := c.GetFreeBalance(ctx, "USDT"); err == nil {
		log.Printf("💳 USDT 可用余额: %.2f, 本次需要: %.2f", freeBalance, totalRequired)
		if freeBalance < totalRequired {
			log.Printf("⚠️ 余额不足！可用 %.2f USDT < 所需 %.2f USDT，部分交易可能失败", freeBalance, totalRequired)
		}
	} else {
		log.Printf("⚠️ 余额预检失败: %v", err)
	}

	results := make([]*TradeResult, 0, len(pairs))

	for i, pair := range pairs {
		pair = strings.TrimSpace(pair)
		amount := strings.TrimSpace(amounts[i])

		log.Printf("📊 正在执行定投: %s, 金额: %s USDT", pair, amount)

		result := c.ExecuteMarketBuy(ctx, pair, amount)
		results = append(results, result)

		if result.Error != nil {
			log.Printf("❌ %s 交易失败: %v", pair, result.Error)
		} else {
			log.Printf("✅ %s 交易成功: 成交 %s, 均价 %s, 花费 %s USDT, 手续费 %s",
				pair, result.FilledQty, result.AvgPrice, result.QuoteAmount, result.Commission)
		}

		// 多笔交易之间间隔 500ms，避免频率限制
		if i < len(pairs)-1 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	return results
}

// GetFreeBalance 获取指定资产的可用余额
func (c *Client) GetFreeBalance(ctx context.Context, asset string) (float64, error) {
	account, err := c.client.NewGetAccountService().Do(ctx)
	if err != nil {
		return 0, fmt.Errorf("查询账户失败: %w", err)
	}

	for _, balance := range account.Balances {
		if balance.Asset == asset {
			free, _ := strconv.ParseFloat(balance.Free, 64)
			return free, nil
		}
	}

	return 0, nil
}

// GetAccountBalance 查询账户 USDT 余额
func (c *Client) GetAccountBalance(ctx context.Context) (string, error) {
	account, err := c.client.NewGetAccountService().Do(ctx)
	if err != nil {
		return "", fmt.Errorf("查询账户失败: %w", err)
	}

	for _, balance := range account.Balances {
		if balance.Asset == "USDT" {
			free, _ := strconv.ParseFloat(balance.Free, 64)
			locked, _ := strconv.ParseFloat(balance.Locked, 64)
			return fmt.Sprintf("%.2f (可用: %s, 冻结: %s)", free+locked, balance.Free, balance.Locked), nil
		}
	}

	return "0.00", nil
}

// AssetBalance 资产余额信息
type AssetBalance struct {
	Asset  string
	Free   float64
	Locked float64
	Total  float64
}

// isVirtualAsset 判断是否为虚拟/凭证代币（非真实现货资产）
// LD 前缀: 活期理财凭证 (如 LDBNB, LDBTC, LDUSDT)
func isVirtualAsset(asset string) bool {
	return strings.HasPrefix(asset, "LD")
}

// GetAllSpotBalances 获取所有非零现货余额（过滤掉 LD 等理财凭证代币）
func (c *Client) GetAllSpotBalances(ctx context.Context) ([]AssetBalance, error) {
	account, err := c.client.NewGetAccountService().Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询账户失败: %w", err)
	}

	var balances []AssetBalance
	for _, b := range account.Balances {
		// 过滤理财凭证代币
		if isVirtualAsset(b.Asset) {
			continue
		}
		free, _ := strconv.ParseFloat(b.Free, 64)
		locked, _ := strconv.ParseFloat(b.Locked, 64)
		total := free + locked
		if total > 0 {
			balances = append(balances, AssetBalance{
				Asset:  b.Asset,
				Free:   free,
				Locked: locked,
				Total:  total,
			})
		}
	}

	return balances, nil
}

// GetSpotBalance 获取指定资产的现货余额详情
func (c *Client) GetSpotBalance(ctx context.Context, asset string) (*AssetBalance, error) {
	account, err := c.client.NewGetAccountService().Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询账户失败: %w", err)
	}

	asset = strings.ToUpper(asset)
	for _, b := range account.Balances {
		if b.Asset == asset {
			free, _ := strconv.ParseFloat(b.Free, 64)
			locked, _ := strconv.ParseFloat(b.Locked, 64)
			return &AssetBalance{
				Asset:  b.Asset,
				Free:   free,
				Locked: locked,
				Total:  free + locked,
			}, nil
		}
	}

	return &AssetBalance{Asset: asset}, nil
}
