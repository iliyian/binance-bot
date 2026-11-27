package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	gobinance "github.com/adshao/go-binance/v2"
	gobinancefutures "github.com/adshao/go-binance/v2/futures"
)

// UniversalTransferResult 跨账户划转结果
type UniversalTransferResult struct {
	Source string  // 来源账户名称 (资金/合约/杠杆)
	Amount float64 // 划转金额
	TranID int64   // 划转交易 ID
	Error  error
}

// TransferSource 划转来源定义 (账户名 + 划转类型 + 余额查询函数)
type TransferSource struct {
	Name         string
	TransferType gobinance.UserUniversalTransferType
	GetBalance   func(ctx context.Context, asset string) (float64, error)
}

// IsUnifiedTransferEnabled 检查是否开启统一账户模式 (统一账户/策略账户)
// 通过 GET /sapi/v1/account/info 的 portiAble 字段判断:
// 统一账户模式下现货/合约/杠杆资产共享，现货下单可直接使用其他账户资产，无需跨账户划转
// 查询失败 (如旧账户无权限) 时返回 false，走正常划转流程
func (c *Client) IsUnifiedTransferEnabled(ctx context.Context) bool {
	body, err := c.signedRequest(ctx, http.MethodGet, "/sapi/v1/account/info", url.Values{})
	if err != nil {
		c.debugLog("🔍 查询账户信息失败 (判断统一账户): %v", err)
		return false
	}

	var info struct {
		PortiAble                      bool `json:"portiAble"`                      // 旧版字段名 (拼写如此)
		IsPortfolioMarginRetailEnabled bool `json:"isPortfolioMarginRetailEnabled"` // 新版字段名
	}
	if err := json.Unmarshal(body, &info); err != nil {
		c.debugLog("🔍 解析账户信息失败 (判断统一账户): %v", err)
		return false
	}

	unified := info.PortiAble || info.IsPortfolioMarginRetailEnabled
	c.debugLog("🔍 统一账户检查: portiAble=%v, isPortfolioMarginRetailEnabled=%v",
		info.PortiAble, info.IsPortfolioMarginRetailEnabled)
	return unified
}

// GetFundingBalance 查询资金账户 (Funding) 指定资产的可用余额
func (c *Client) GetFundingBalance(ctx context.Context, asset string) (float64, error) {
	params := url.Values{}
	params.Set("asset", asset)

	// 注意: 该接口仅支持 POST，用 GET 会返回 -1000 "Request method 'GET' is not supported"
	body, err := c.signedRequest(ctx, http.MethodPost, "/sapi/v1/asset/get-funding-asset", params)
	if err != nil {
		return 0, fmt.Errorf("查询资金账户余额失败: %w", err)
	}

	var resp []struct {
		Asset string `json:"asset"`
		Free  string `json:"free"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("解析资金账户余额失败: %w", err)
	}

	for _, a := range resp {
		if a.Asset == asset {
			free, _ := strconv.ParseFloat(a.Free, 64)
			return free, nil
		}
	}
	return 0, nil
}

// futuresClient 惰性创建 USDT 合约客户端 (首次使用时同步服务器时间)
func (c *Client) futuresClient() *gobinancefutures.Client {
	c.futuresOnce.Do(func() {
		c.futuresCli = gobinancefutures.NewClient(c.apiKey, c.secretKey)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := c.futuresCli.NewSetServerTimeService().Do(ctx); err != nil {
			log.Printf("⚠️ 合约客户端时间同步失败: %v", err)
		}
	})
	return c.futuresCli
}

// GetFuturesAvailableBalance 查询 USDT 合约账户指定资产的可用余额
func (c *Client) GetFuturesAvailableBalance(ctx context.Context, asset string) (float64, error) {
	balances, err := c.futuresClient().NewGetBalanceService().Do(ctx)
	if err != nil {
		return 0, fmt.Errorf("查询合约账户余额失败: %w", err)
	}

	for _, b := range balances {
		if b.Asset == asset {
			avail, _ := strconv.ParseFloat(b.AvailableBalance, 64)
			return avail, nil
		}
	}
	return 0, nil
}

// GetMarginFreeBalance 查询全仓杠杆账户指定资产的可用余额
func (c *Client) GetMarginFreeBalance(ctx context.Context, asset string) (float64, error) {
	account, err := c.client.NewGetMarginAccountService().Do(ctx)
	if err != nil {
		return 0, fmt.Errorf("查询全仓杠杆账户失败: %w", err)
	}

	for _, a := range account.UserAssets {
		if a.Asset == asset {
			free, _ := strconv.ParseFloat(a.Free, 64)
			return free, nil
		}
	}
	return 0, nil
}

// TransferSourceByName 根据名称返回划转来源定义
// 支持: funding (资金账户) / umfuture (USDT 合约账户) / margin (全仓杠杆账户)
func (c *Client) TransferSourceByName(name string) *TransferSource {
	switch name {
	case "funding":
		return &TransferSource{
			Name:         "资金账户",
			TransferType: gobinance.UserUniversalTransferTypeFundingToMain,
			GetBalance:   c.GetFundingBalance,
		}
	case "umfuture":
		return &TransferSource{
			Name:         "USDT 合约账户",
			TransferType: gobinance.UserUniversalTransferTypeUmFuturesToMain,
			GetBalance:   c.GetFuturesAvailableBalance,
		}
	case "margin":
		return &TransferSource{
			Name:         "全仓杠杆账户",
			TransferType: gobinance.UserUniversalTransferTypeMarginToMain,
			GetBalance:   c.GetMarginFreeBalance,
		}
	}
	return nil
}

// UniversalTransfer 执行通用划转 (POST /sapi/v1/asset/transfer)
func (c *Client) UniversalTransfer(ctx context.Context, transferType gobinance.UserUniversalTransferType, asset string, amount float64) (int64, error) {
	resp, err := c.client.NewUserUniversalTransferService().
		Type(transferType).
		Asset(asset).
		Amount(amount).
		Do(ctx)
	if err != nil {
		return 0, err
	}
	return resp.ID, nil
}

// AutoTransferIfNeeded 现货 USDT 余额不足时，按配置的顺序从其他账户划转到现货账户
// sources 为空时不做任何划转
// 若已开启统一账户模式 (portiAble=true)，资产在账户间共享，自动跳过划转
func (c *Client) AutoTransferIfNeeded(ctx context.Context, pairs, amounts []string, sources []TransferSource) []*UniversalTransferResult {
	if len(sources) == 0 {
		return nil
	}

	var results []*UniversalTransferResult

	// 计算总共需要的 USDT
	totalRequired := 0.0
	for _, amt := range amounts {
		v, _ := strconv.ParseFloat(strings.TrimSpace(amt), 64)
		totalRequired += v
	}

	// 查询现货 USDT 可用余额
	freeBalance, err := c.GetFreeBalance(ctx, "USDT")
	if err != nil {
		log.Printf("⚠️ 查询现货余额失败，跳过跨账户划转: %v", err)
		return nil
	}

	needAmount := totalRequired - freeBalance
	if needAmount <= 0 {
		log.Printf("💰 现货 USDT 余额充足 (%.2f >= %.2f)，无需跨账户划转", freeBalance, totalRequired)
		return nil
	}

	log.Printf("💸 现货 USDT 余额不足 (%.2f < %.2f)，需补足 %.2f USDT", freeBalance, totalRequired, needAmount)

	// 统一账户检查: portiAble=true 时各账户资产共享，无需划转
	if c.IsUnifiedTransferEnabled(ctx) {
		log.Printf("ℹ️ 已开启统一账户模式，资产在账户间共享，无需跨账户划转")
		return nil
	}

	// 按配置顺序依次尝试划转
	for _, src := range sources {
		if needAmount <= 0 {
			break
		}

		avail, err := src.GetBalance(ctx, "USDT")
		if err != nil {
			log.Printf("⚠️ 查询%s USDT 余额失败: %v", src.Name, err)
			results = append(results, &UniversalTransferResult{Source: src.Name, Error: err})
			continue
		}

		if avail <= 0 {
			log.Printf("ℹ️ %s 无可用 USDT，跳过", src.Name)
			continue
		}

		transferAmount := needAmount
		if transferAmount > avail {
			transferAmount = avail
		}
		// 截断到 8 位小数，避免浮点误差导致 API 拒绝
		transferAmount = math.Floor(transferAmount*1e8) / 1e8

		log.Printf("🔄 从%s划转 %.8f USDT 到现货账户...", src.Name, transferAmount)
		tranID, err := c.UniversalTransfer(ctx, src.TransferType, "USDT", transferAmount)
		result := &UniversalTransferResult{
			Source: src.Name,
			Amount: transferAmount,
			TranID: tranID,
			Error:  err,
		}
		results = append(results, result)

		if err != nil {
			log.Printf("❌ 从%s划转失败: %v", src.Name, err)
			continue
		}

		log.Printf("✅ 成功从%s划转 %.8f USDT 到现货 (tranId: %d)", src.Name, transferAmount, tranID)
		needAmount -= transferAmount

		// 等待划转到账
		time.Sleep(1 * time.Second)
	}

	if needAmount > 0 {
		log.Printf("⚠️ 跨账户划转后仍缺 %.2f USDT，本次定投可能部分失败", needAmount)
	}

	return results
}

// PostTradeTransferResult 定投成交后划转结果
type PostTradeTransferResult struct {
	Asset  string  // 划转资产 (基础货币)
	Amount float64 // 划转数量
	Target string  // 目标账户描述
	TranID int64   // 划转交易 ID
	Error  error
}

// postTradeTransferType 目标账户 → 划转类型 / 显示名
// 仅支持从现货账户 (MAIN) 转出
func postTradeTransferType(target string) (gobinance.UserUniversalTransferType, string, bool) {
	switch target {
	case "umfuture":
		return gobinance.UserUniversalTransferTypeMainToUmFutures, "USDT 合约账户", true
	case "cmfuture":
		return gobinance.UserUniversalTransferTypeMainToCmFutures, "币本位合约账户", true
	case "margin":
		return gobinance.UserUniversalTransferTypeMainToMargin, "全仓杠杆账户", true
	case "funding":
		return gobinance.UserUniversalTransferTypeMainToFunding, "资金账户", true
	}
	return "", "", false
}

// baseAssetOf 从交易对提取基础货币，如 BTCUSDT → BTC、ETHBTC → ETH
func baseAssetOf(symbol string) string {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	// 必须严格按长度降序：长后缀先匹配，避免短后缀误伤
	// （如 ETHBTC 若先匹配到 BTC 之外的短串就没法正确拆出 ETH）
	quotes := []string{"FDUSD", "USDT", "USDC", "TUSD", "BUSD", "BNB", "BTC", "ETH", "EUR", "TRY"}
	for _, q := range quotes {
		if strings.HasSuffix(symbol, q) && len(symbol) > len(q) {
			return strings.TrimSuffix(symbol, q)
		}
	}
	return ""
}

// AutoTransferAfterTrades 定投成交后，把本次买入的基础货币从现货划转到目标账户
// target: umfuture / cmfuture / margin / funding；assetFilter 非空时仅划转白名单资产
// minAmount > 0 时，低于该数量的资产跳过
// 单笔失败只记录错误，不中断后续资产
func (c *Client) AutoTransferAfterTrades(ctx context.Context, tradeResults []*TradeResult, target string, assetFilter []string, minAmount float64) []*PostTradeTransferResult {
	transferType, targetName, ok := postTradeTransferType(target)
	if !ok {
		return nil
	}

	// 统一账户模式下资产共享，无需划转
	if c.IsUnifiedTransferEnabled(ctx) {
		log.Printf("ℹ️ 已开启统一账户模式，定投后划转跳过 (资产在账户间共享)")
		return nil
	}

	// 允许划转的资产白名单
	allowed := make(map[string]bool, len(assetFilter))
	for _, a := range assetFilter {
		allowed[strings.ToUpper(strings.TrimSpace(a))] = true
	}

	// 汇总本次买入的基础货币数量（同一基础货币可能来自多个交易对）
	type want struct {
		symbol string
		qty    float64
	}
	wants := make(map[string]*want)
	var order []string
	for _, r := range tradeResults {
		if r.Error != nil {
			continue
		}
		base := baseAssetOf(r.Symbol)
		if base == "" {
			continue
		}
		if len(allowed) > 0 && !allowed[base] {
			continue
		}
		qty, err := strconv.ParseFloat(r.FilledQty, 64)
		if err != nil || qty <= 0 {
			continue
		}
		if w, exist := wants[base]; exist {
			w.qty += qty
		} else {
			wants[base] = &want{symbol: r.Symbol, qty: qty}
			order = append(order, base)
		}
	}

	if len(order) == 0 {
		return nil
	}

	var results []*PostTradeTransferResult
	for _, asset := range order {
		w := wants[asset]
		// 以实际现货可用余额为上限，避免把历史持仓一并扫走
		free, err := c.GetFreeBalance(ctx, asset)
		if err != nil {
			log.Printf("⚠️ 查询 %s 现货余额失败，跳过划转: %v", asset, err)
			results = append(results, &PostTradeTransferResult{Asset: asset, Target: targetName, Error: err})
			continue
		}

		amount := w.qty
		if free < amount {
			amount = free
		}
		// 截断到 8 位小数，避免浮点误差
		amount = math.Floor(amount*1e8) / 1e8

		if amount <= 0 {
			log.Printf("ℹ️ %s 现货可用余额为 0，跳过划转", asset)
			continue
		}
		if minAmount > 0 && amount < minAmount {
			log.Printf("ℹ️ %s 划转数量 %.8f 低于门槛 %.8f，跳过", asset, amount, minAmount)
			results = append(results, &PostTradeTransferResult{Asset: asset, Amount: amount, Target: targetName,
				Error: fmt.Errorf("低于划转门槛 %.8f", minAmount)})
			continue
		}

		log.Printf("🔄 定投后划转: %.8f %s → %s ...", amount, asset, targetName)
		tranID, err := c.UniversalTransfer(ctx, transferType, asset, amount)
		results = append(results, &PostTradeTransferResult{
			Asset:  asset,
			Amount: amount,
			Target: targetName,
			TranID: tranID,
			Error:  err,
		})
		if err != nil {
			log.Printf("❌ %s 划转到%s失败: %v", asset, targetName, err)
			continue
		}
		log.Printf("✅ 已划转 %.8f %s 到%s (tranId: %d)", amount, asset, targetName, tranID)

		// 等待划转到账，避免下一笔触发频率限制
		time.Sleep(1 * time.Second)
	}

	return results
}
