package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// EarnTransferResult 理财划转结果
type EarnTransferResult struct {
	Asset     string
	Amount    float64
	Direction string // "赎回" 或 "申购"
	ProductID string
	Error     error
}

// SimpleEarnPosition Simple Earn 活期持仓
type SimpleEarnPosition struct {
	ProductId                string `json:"productId"`
	Asset                    string `json:"asset"`
	TotalAmount              string `json:"totalAmount"`
	FreeAmount               string `json:"freeAmount"`
	CollateralAmount         string `json:"collateralAmount"`
	CanRedeem                bool   `json:"canRedeem"`
	LatestAnnualPercentageRate string `json:"latestAnnualPercentageRate"`
	CumulativeTotalRewards   string `json:"cumulativeTotalRewards"`
	AutoSubscribe            bool   `json:"autoSubscribe"`
}

// GetRedeemableAmount 获取可赎回金额（优先 freeAmount，否则 totalAmount - collateralAmount）
func (p *SimpleEarnPosition) GetRedeemableAmount() string {
	if p.FreeAmount != "" && p.FreeAmount != "0" {
		return p.FreeAmount
	}
	// collateralAmount 为质押锁定金额，可赎回 = 总量 - 质押
	if p.CollateralAmount != "" && p.CollateralAmount != "0" {
		total, _ := strconv.ParseFloat(p.TotalAmount, 64)
		collateral, _ := strconv.ParseFloat(p.CollateralAmount, 64)
		redeemable := total - collateral
		if redeemable < 0 {
			redeemable = 0
		}
		return strconv.FormatFloat(redeemable, 'f', 8, 64)
	}
	return p.TotalAmount
}

// SimpleEarnPositionResponse Simple Earn 持仓查询响应
type SimpleEarnPositionResponse struct {
	Rows  []SimpleEarnPosition `json:"rows"`
	Total int                  `json:"total"`
}

// SimpleEarnProduct Simple Earn 活期产品
type SimpleEarnProduct struct {
	ProductId  string `json:"productId"`
	Asset      string `json:"asset"`
	Status     string `json:"status"`
	CanRedeem  bool   `json:"canRedeem"`
	MinPurchaseAmount string `json:"minPurchaseAmount"`
}

// SimpleEarnProductResponse Simple Earn 产品列表响应
type SimpleEarnProductResponse struct {
	Rows  []SimpleEarnProduct `json:"rows"`
	Total int                 `json:"total"`
}

// SimpleEarnRedeemResponse 赎回响应
type SimpleEarnRedeemResponse struct {
	RedeemId int64 `json:"redeemId"`
	Success  bool  `json:"success"`
}

// SimpleEarnSubscribeResponse 申购响应
type SimpleEarnSubscribeResponse struct {
	PurchaseId int64 `json:"purchaseId"`
	Success    bool  `json:"success"`
}

// signedRequest 发送签名请求到 Binance API
func (c *Client) signedRequest(ctx context.Context, method, endpoint string, params url.Values) ([]byte, error) {
	if params == nil {
		params = url.Values{}
	}

	// 添加时间戳
	timestamp := time.Now().UnixMilli() - c.client.TimeOffset
	params.Set("timestamp", strconv.FormatInt(timestamp, 10))

	// HMAC-SHA256 签名
	mac := hmac.New(sha256.New, []byte(c.secretKey))
	mac.Write([]byte(params.Encode()))
	signature := hex.EncodeToString(mac.Sum(nil))
	params.Set("signature", signature)

	// 构建完整 URL
	fullURL := fmt.Sprintf("%s%s", c.client.BaseURL, endpoint)

	var req *http.Request
	var err error

	if method == http.MethodGet {
		fullURL = fullURL + "?" + params.Encode()
		req, err = http.NewRequestWithContext(ctx, method, fullURL, nil)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, fullURL, strings.NewReader(params.Encode()))
		if req != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("X-MBX-APIKEY", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API 错误 %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// GetFlexiblePositions 查询 Simple Earn 活期持仓
func (c *Client) GetFlexiblePositions(ctx context.Context, asset string) ([]SimpleEarnPosition, error) {
	params := url.Values{}
	if asset != "" {
		params.Set("asset", asset)
	}
	params.Set("size", "100")

	body, err := c.signedRequest(ctx, http.MethodGet, "/sapi/v1/simple-earn/flexible/position", params)
	if err != nil {
		return nil, err
	}

	c.debugLog("🔍 活期持仓原始响应: %s", string(body))

	var resp SimpleEarnPositionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析持仓响应失败: %w", err)
	}

	return resp.Rows, nil
}

// RedeemFlexible 从 Simple Earn 活期赎回
func (c *Client) RedeemFlexible(ctx context.Context, productId string, amount float64) (*SimpleEarnRedeemResponse, error) {
	params := url.Values{}
	params.Set("productId", productId)
	if amount > 0 {
		params.Set("amount", strconv.FormatFloat(amount, 'f', 8, 64))
	} else {
		// amount 不传表示全部赎回
		params.Set("redeemAll", "true")
	}

	body, err := c.signedRequest(ctx, http.MethodPost, "/sapi/v1/simple-earn/flexible/redeem", params)
	if err != nil {
		return nil, err
	}

	var resp SimpleEarnRedeemResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析赎回响应失败: %w", err)
	}

	return &resp, nil
}

// SubscribeFlexible 申购 Simple Earn 活期产品
func (c *Client) SubscribeFlexible(ctx context.Context, productId string, amount float64) (*SimpleEarnSubscribeResponse, error) {
	params := url.Values{}
	params.Set("productId", productId)
	params.Set("amount", strconv.FormatFloat(amount, 'f', 8, 64))

	body, err := c.signedRequest(ctx, http.MethodPost, "/sapi/v1/simple-earn/flexible/subscribe", params)
	if err != nil {
		return nil, err
	}

	var resp SimpleEarnSubscribeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析申购响应失败: %w", err)
	}

	return &resp, nil
}

// GetFlexibleProducts 查询 Simple Earn 活期产品列表
func (c *Client) GetFlexibleProducts(ctx context.Context, asset string) ([]SimpleEarnProduct, error) {
	params := url.Values{}
	if asset != "" {
		params.Set("asset", asset)
	}
	params.Set("size", "100")

	body, err := c.signedRequest(ctx, http.MethodGet, "/sapi/v1/simple-earn/flexible/list", params)
	if err != nil {
		return nil, err
	}

	var resp SimpleEarnProductResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析产品列表响应失败: %w", err)
	}

	return resp.Rows, nil
}

// RedeemFlexibleSavings 从活期理财赎回资产到现货账户
func (c *Client) RedeemFlexibleSavings(ctx context.Context, asset string, amount float64) *EarnTransferResult {
	result := &EarnTransferResult{
		Asset:     asset,
		Amount:    amount,
		Direction: "赎回",
	}

	// 1. 查询活期持仓
	positions, err := c.GetFlexiblePositions(ctx, asset)
	if err != nil {
		result.Error = fmt.Errorf("查询 %s 活期理财持仓失败: %w", asset, err)
		return result
	}

	if len(positions) == 0 {
		result.Error = fmt.Errorf("%s 没有活期理财持仓", asset)
		return result
	}

	// 遍历所有持仓，找到可赎回的
	var pos SimpleEarnPosition
	var freeAmount float64
	found := false
	for _, p := range positions {
		c.debugLog("🔍 持仓详情: productId=%s, asset=%s, totalAmount=%s, freeAmount=%s, canRedeem=%v",
			p.ProductId, p.Asset, p.TotalAmount, p.FreeAmount, p.CanRedeem)

		fa, _ := strconv.ParseFloat(p.FreeAmount, 64)
		ta, _ := strconv.ParseFloat(p.TotalAmount, 64)

		// 优先使用 freeAmount，如果为 0 则回退到 totalAmount
		available := fa
		if available <= 0 {
			available = ta
		}

		if available > 0 && p.CanRedeem {
			pos = p
			freeAmount = available
			found = true
			break
		}
	}

	if !found {
		result.Error = fmt.Errorf("%s 活期理财可赎回余额为 0 (共 %d 个持仓)", asset, len(positions))
		return result
	}

	// 实际赎回金额取 min(需要金额, 可用金额)
	redeemAmount := amount
	if redeemAmount > freeAmount {
		redeemAmount = freeAmount
		log.Printf("⚠️ %s 活期理财余额 %.8f 不足 %.8f，将全额赎回", asset, freeAmount, amount)
	}

	result.Amount = redeemAmount
	result.ProductID = pos.ProductId

	// 2. 赎回
	resp, err := c.RedeemFlexible(ctx, pos.ProductId, redeemAmount)
	if err != nil {
		result.Error = fmt.Errorf("赎回 %s 活期理财失败: %w", asset, err)
		return result
	}

	if !resp.Success {
		result.Error = fmt.Errorf("赎回 %s 活期理财返回失败", asset)
		return result
	}

	log.Printf("✅ 成功从活期理财赎回 %.8f %s (redeemId: %d)", redeemAmount, asset, resp.RedeemId)
	return result
}

// PurchaseFlexibleSavings 将现货资产申购到活期理财
func (c *Client) PurchaseFlexibleSavings(ctx context.Context, asset string, amount float64) *EarnTransferResult {
	result := &EarnTransferResult{
		Asset:     asset,
		Amount:    amount,
		Direction: "申购",
	}

	// 1. 查询可申购的活期产品
	products, err := c.GetFlexibleProducts(ctx, asset)
	if err != nil {
		result.Error = fmt.Errorf("查询 %s 活期理财产品失败: %w", asset, err)
		return result
	}

	var productId string
	var minPurchase float64
	for _, p := range products {
		if p.Asset == asset && p.Status == "PURCHASING" {
			productId = p.ProductId
			minPurchase, _ = strconv.ParseFloat(p.MinPurchaseAmount, 64)
			break
		}
	}

	if productId == "" {
		result.Error = fmt.Errorf("%s 没有可申购的活期理财产品", asset)
		return result
	}

	if amount < minPurchase {
		result.Error = fmt.Errorf("%s 申购金额 %.8f 低于最小申购 %.8f", asset, amount, minPurchase)
		return result
	}

	result.ProductID = productId

	// 2. 申购
	resp, err := c.SubscribeFlexible(ctx, productId, amount)
	if err != nil {
		result.Error = fmt.Errorf("申购 %s 活期理财失败: %w", asset, err)
		return result
	}

	if !resp.Success {
		result.Error = fmt.Errorf("申购 %s 活期理财返回失败", asset)
		return result
	}

	log.Printf("✅ 成功申购 %.8f %s 到活期理财 (purchaseId: %d)", amount, asset, resp.PurchaseId)
	return result
}

// AutoRedeemIfNeeded 余额不足时自动从活期理财赎回
func (c *Client) AutoRedeemIfNeeded(ctx context.Context, pairs []string, amounts []string) []*EarnTransferResult {
	var results []*EarnTransferResult

	// 计算总共需要的 USDT
	totalRequired := 0.0
	for _, amt := range amounts {
		v, _ := strconv.ParseFloat(strings.TrimSpace(amt), 64)
		totalRequired += v
	}

	// 查询现货 USDT 余额
	freeBalance, err := c.GetFreeBalance(ctx, "USDT")
	if err != nil {
		log.Printf("⚠️ 查询余额失败，跳过自动赎回: %v", err)
		return nil
	}

	if freeBalance >= totalRequired {
		log.Printf("💰 USDT 余额充足 (%.2f >= %.2f)，无需从理财赎回", freeBalance, totalRequired)
		return nil
	}

	// 需要赎回的金额（仅赎回差额，不多赎）
	needAmount := totalRequired - freeBalance
	log.Printf("💸 USDT 余额不足 (%.2f < %.2f)，需从活期理财赎回 %.2f USDT", freeBalance, totalRequired, needAmount)

	result := c.RedeemFlexibleSavings(ctx, "USDT", needAmount)
	results = append(results, result)

	if result.Error != nil {
		log.Printf("❌ 自动赎回失败: %v", result.Error)
	} else {
		// 等待赎回到账
		log.Printf("⏳ 等待赎回到账...")
		time.Sleep(2 * time.Second)
	}

	return results
}

// AutoPurchaseToSavings 交易完成后自动将资产申购到活期理财
func (c *Client) AutoPurchaseToSavings(ctx context.Context, tradeResults []*TradeResult) []*EarnTransferResult {
	var results []*EarnTransferResult

	// 收集需要申购的资产
	assetsToSave := make(map[string]bool)
	for _, r := range tradeResults {
		if r.Error != nil {
			continue
		}
		// 从交易对中提取基础货币 (如 BTCUSDT -> BTC)
		baseAsset := strings.TrimSuffix(r.Symbol, "USDT")
		if baseAsset != r.Symbol {
			assetsToSave[baseAsset] = true
		}
	}
	// USDT 也需要存入
	assetsToSave["USDT"] = true

	for asset := range assetsToSave {
		// 查询该资产在现货的可用余额
		freeBalance, err := c.GetFreeBalance(ctx, asset)
		if err != nil {
			log.Printf("⚠️ 查询 %s 余额失败: %v", asset, err)
			continue
		}

		if freeBalance <= 0 {
			continue
		}

		log.Printf("📤 将 %.8f %s 申购到活期理财", freeBalance, asset)
		result := c.PurchaseFlexibleSavings(ctx, asset, freeBalance)
		results = append(results, result)

		if result.Error != nil {
			log.Printf("⚠️ %s 申购失败: %v", asset, result.Error)
		}

		time.Sleep(500 * time.Millisecond)
	}

	return results
}
