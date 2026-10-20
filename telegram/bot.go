package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/iliyian/binance-bot/binance"
)

// PoolGetter 获取当前 pool 状态的回调函数类型
type PoolGetter func() map[string]float64

// PoolSetter 设置指定交易对 pool 值的回调函数类型
type PoolSetter func(pair string, amount float64) error

// EnvUpdater 将当前 pool 状态同步写入 .env 的回调函数类型
type EnvUpdater func() error

// MonitorStatusGetter 获取监控状态的回调函数类型
type MonitorStatusGetter func() string

// MonitorCheckNow 立即执行监控检查并返回结果的回调函数类型
type MonitorCheckNow func() string

// Bot Telegram Bot 命令处理器
type Bot struct {
	botToken      string
	chatID        string
	client        *http.Client
	binance       *binance.Client
	cancel        context.CancelFunc
	poolGetter    PoolGetter
	poolSetter    PoolSetter
	envUpdater    EnvUpdater
	monitorGetter MonitorStatusGetter
	monitorCheck  MonitorCheckNow

	// 监控交易对的读取/写入回调（写入方负责校验、热更新和同步 .env）
	monitorSymbolsGetter func() []string
	monitorSymbolsSetter func([]string) error
	pendingAdd           map[int64]bool
}

// TelegramUpdate Telegram 更新结构
type TelegramUpdate struct {
	UpdateID      int              `json:"update_id"`
	Message       *TelegramMessage `json:"message"`
	CallbackQuery *CallbackQuery   `json:"callback_query"`
}

// CallbackQuery 内联按钮回调
type CallbackQuery struct {
	ID      string           `json:"id"`
	From    *TelegramUser    `json:"from"`
	Message *TelegramMessage `json:"message"`
	Data    string           `json:"data"`
}

// TelegramMessage Telegram 消息结构
type TelegramMessage struct {
	MessageID      int              `json:"message_id"`
	From           *TelegramUser    `json:"from"`
	Chat           *TelegramChat    `json:"chat"`
	Text           string           `json:"text"`
	Entities       []MessageEntity  `json:"entities"`
	ReplyToMessage *TelegramMessage `json:"reply_to_message"`
}

// TelegramUser Telegram 用户
type TelegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// TelegramChat Telegram 聊天
type TelegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// MessageEntity 消息实体（用于识别命令）
type MessageEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

// BotCommand Telegram Bot 命令定义
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// GetUpdatesResponse getUpdates 响应
type GetUpdatesResponse struct {
	OK     bool             `json:"ok"`
	Result []TelegramUpdate `json:"result"`
}

// NewBot 创建 Telegram Bot
func NewBot(botToken, chatID string, binanceClient *binance.Client) *Bot {
	return &Bot{
		botToken: botToken,
		chatID:   chatID,
		client: &http.Client{
			Timeout: 60 * time.Second, // 长轮询超时
		},
		binance:    binanceClient,
		pendingAdd: make(map[int64]bool),
	}
}

// SetPoolGetter 设置 pool 数据获取回调
func (b *Bot) SetPoolGetter(getter PoolGetter) {
	b.poolGetter = getter
}

// SetPoolSetter 设置 pool 数据写入回调
func (b *Bot) SetPoolSetter(setter PoolSetter) {
	b.poolSetter = setter
}

// SetEnvUpdater 设置 .env 同步回调
func (b *Bot) SetEnvUpdater(updater EnvUpdater) {
	b.envUpdater = updater
}

// SetMonitorStatusGetter 设置监控状态获取回调
func (b *Bot) SetMonitorStatusGetter(getter MonitorStatusGetter) {
	b.monitorGetter = getter
}

// SetMonitorCheckNow 设置立即执行监控检查的回调
func (b *Bot) SetMonitorCheckNow(checker MonitorCheckNow) {
	b.monitorCheck = checker
}

// SetMonitorSymbols 设置监控交易对的读取与写入回调
func (b *Bot) SetMonitorSymbols(getter func() []string, setter func([]string) error) {
	b.monitorSymbolsGetter = getter
	b.monitorSymbolsSetter = setter
}

// Start 启动 Bot 长轮询
func (b *Bot) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel

	// 注册命令
	if err := b.registerCommands(); err != nil {
		log.Printf("⚠️ Telegram Bot 命令注册失败: %v", err)
	} else {
		log.Println("✅ Telegram Bot 命令注册成功")
	}

	// 启动长轮询
	go b.pollUpdates(ctx)
	log.Println("🤖 Telegram Bot 命令监听已启动")
}

// Stop 停止 Bot
func (b *Bot) Stop() {
	if b.cancel != nil {
		b.cancel()
	}
	log.Println("⏹ Telegram Bot 命令监听已停止")
}

// registerCommands 注册 Bot 命令到 Telegram
func (b *Bot) registerCommands() error {
	commands := []BotCommand{
		{Command: "balance", Description: "查询所有账户余额总览"},
		{Command: "spot", Description: "查询现货账户余额"},
		{Command: "earn", Description: "查询活期理财持仓"},
		{Command: "asset", Description: "查询指定币种余额，用法: /asset BTC"},
		{Command: "pool", Description: "查询各交易对的 Pool 累积金额"},
		{Command: "setpool", Description: "设置 Pool 金额，用法: /setpool BTCUSDT 0.5"},
		{Command: "monitor", Description: "查询价格监控状态"},
		{Command: "help", Description: "显示帮助信息"},
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/setMyCommands", b.botToken)

	payload := map[string]interface{}{
		"commands": commands,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化命令失败: %w", err)
	}

	resp, err := b.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("注册命令失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("注册命令 API 错误 %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// pollUpdates 长轮询获取更新
func (b *Bot) pollUpdates(ctx context.Context) {
	offset := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := b.getUpdates(offset, 30)
		if err != nil {
			if ctx.Err() != nil {
				return // 上下文已取消
			}
			log.Printf("⚠️ 获取 Telegram 更新失败: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		for _, update := range updates {
			offset = update.UpdateID + 1
			b.handleUpdate(update)
		}
	}
}

// getUpdates 获取更新
func (b *Bot) getUpdates(offset, timeout int) ([]TelegramUpdate, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=%d&allowed_updates=[\"message\",\"callback_query\"]",
		b.botToken, offset, timeout)

	resp, err := b.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result GetUpdatesResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	if !result.OK {
		return nil, fmt.Errorf("getUpdates 返回失败: %s", string(body))
	}

	return result.Result, nil
}

// handleUpdate 处理更新
func (b *Bot) handleUpdate(update TelegramUpdate) {
	if update.CallbackQuery != nil {
		b.handleCallbackQuery(update.CallbackQuery)
		return
	}
	if update.Message == nil {
		return
	}

	msg := update.Message

	// 验证聊天 ID（安全检查）
	chatID := fmt.Sprintf("%d", msg.Chat.ID)
	if chatID != b.chatID {
		log.Printf("⚠️ 忽略来自未授权聊天的消息: chatID=%s", chatID)
		return
	}

	// 非命令文本：如果处于「添加监控交易对」的等待状态，则当作输入处理
	if !strings.HasPrefix(msg.Text, "/") {
		if b.pendingAdd[msg.Chat.ID] {
			delete(b.pendingAdd, msg.Chat.ID)
			b.handlePendingAdd(msg.Text)
		}
		return
	}

	// 解析命令和参数
	parts := strings.Fields(msg.Text)
	command := strings.Split(parts[0], "@")[0] // 去除 @botname
	args := parts[1:]

	log.Printf("🤖 收到命令: %s, 参数: %v", command, args)

	switch command {
	case "/balance":
		b.handleBalance()
	case "/spot":
		b.handleSpot()
	case "/earn":
		b.handleEarn()
	case "/asset":
		b.handleAsset(args)
	case "/pool":
		b.handlePool()
	case "/setpool":
		b.handleSetPool(args)
	case "/monitor":
		b.handleMonitor(args)
	case "/help", "/start":
		b.handleHelp()
	default:
		log.Printf("🤖 未知命令: %s", command)
	}
}

// sendReply 发送回复消息
func (b *Bot) sendReply(text string) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.botToken)

	payload := map[string]interface{}{
		"chat_id":    b.chatID,
		"text":       text,
		"parse_mode": "HTML",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("❌ 序列化回复失败: %v", err)
		return
	}

	resp, err := b.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("❌ 发送回复失败: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("❌ 发送回复 API 错误 %d: %s", resp.StatusCode, string(respBody))
	}
}

// handleBalance 处理 /balance 命令 — 查询所有账户余额总览
func (b *Bot) handleBalance() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var sb strings.Builder
	sb.WriteString("💰 <b>账户余额总览</b>\n")
	sb.WriteString(fmt.Sprintf("📅 %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	// 查询现货余额
	spotBalances, spotErr := b.binance.GetAllSpotBalances(ctx)
	if spotErr != nil {
		sb.WriteString(fmt.Sprintf("❌ 现货账户查询失败: %s\n\n", html.EscapeString(spotErr.Error())))
	} else {
		sb.WriteString("📊 <b>现货账户:</b>\n")
		if len(spotBalances) == 0 {
			sb.WriteString("   (空)\n")
		}
		for _, bal := range spotBalances {
			sb.WriteString(fmt.Sprintf("   • <b>%s</b>: %.8g", bal.Asset, bal.Total))
			if bal.Locked > 0 {
				sb.WriteString(fmt.Sprintf(" (可用: %.8g, 冻结: %.8g)", bal.Free, bal.Locked))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	// 查询活期理财持仓
	earnPositions, earnErr := b.binance.GetFlexiblePositions(ctx, "")
	if earnErr != nil {
		sb.WriteString(fmt.Sprintf("❌ 活期理财查询失败: %s\n", html.EscapeString(earnErr.Error())))
	} else {
		sb.WriteString("🏦 <b>活期理财:</b>\n")
		if len(earnPositions) == 0 {
			sb.WriteString("   (空)\n")
		}
		for _, pos := range earnPositions {
			redeemable := pos.GetRedeemableAmount()
			sb.WriteString(fmt.Sprintf("   • <b>%s</b>: %s", pos.Asset, pos.TotalAmount))
			if redeemable != pos.TotalAmount {
				sb.WriteString(fmt.Sprintf(" (可赎回: %s)", redeemable))
			}
			sb.WriteString("\n")
		}
	}

	b.sendReply(sb.String())
}

// handleSpot 处理 /spot 命令 — 查询现货账户余额
func (b *Bot) handleSpot() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var sb strings.Builder
	sb.WriteString("📊 <b>现货账户余额</b>\n")
	sb.WriteString(fmt.Sprintf("📅 %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	balances, err := b.binance.GetAllSpotBalances(ctx)
	if err != nil {
		sb.WriteString(fmt.Sprintf("❌ 查询失败: %s\n", html.EscapeString(err.Error())))
		b.sendReply(sb.String())
		return
	}

	if len(balances) == 0 {
		sb.WriteString("(无持仓)\n")
		b.sendReply(sb.String())
		return
	}

	for _, bal := range balances {
		sb.WriteString(fmt.Sprintf("• <b>%s</b>\n", bal.Asset))
		sb.WriteString(fmt.Sprintf("   总计: %.8g\n", bal.Total))
		sb.WriteString(fmt.Sprintf("   可用: %.8g\n", bal.Free))
		if bal.Locked > 0 {
			sb.WriteString(fmt.Sprintf("   冻结: %.8g\n", bal.Locked))
		}
	}

	b.sendReply(sb.String())
}

// handleEarn 处理 /earn 命令 — 查询活期理财持仓
func (b *Bot) handleEarn() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var sb strings.Builder
	sb.WriteString("🏦 <b>活期理财持仓</b>\n")
	sb.WriteString(fmt.Sprintf("📅 %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	positions, err := b.binance.GetFlexiblePositions(ctx, "")
	if err != nil {
		sb.WriteString(fmt.Sprintf("❌ 查询失败: %s\n", html.EscapeString(err.Error())))
		b.sendReply(sb.String())
		return
	}

	if len(positions) == 0 {
		sb.WriteString("(无持仓)\n")
		b.sendReply(sb.String())
		return
	}

	for _, pos := range positions {
		redeemable := pos.GetRedeemableAmount()
		sb.WriteString(fmt.Sprintf("• <b>%s</b>\n", pos.Asset))
		sb.WriteString(fmt.Sprintf("   总量: %s\n", pos.TotalAmount))
		sb.WriteString(fmt.Sprintf("   可赎回: %s\n", redeemable))
		sb.WriteString(fmt.Sprintf("   可赎回状态: %s\n", boolToEmoji(pos.CanRedeem)))
		if pos.LatestAnnualPercentageRate != "" {
			sb.WriteString(fmt.Sprintf("   年化利率: %s%%\n", pos.LatestAnnualPercentageRate))
		}
		if pos.CumulativeTotalRewards != "" && pos.CumulativeTotalRewards != "0" {
			sb.WriteString(fmt.Sprintf("   累计收益: %s\n", pos.CumulativeTotalRewards))
		}
		sb.WriteString(fmt.Sprintf("   产品ID: <code>%s</code>\n", pos.ProductId))
	}

	b.sendReply(sb.String())
}

// handleAsset 处理 /asset 命令 — 查询指定币种在所有账户的余额
func (b *Bot) handleAsset(args []string) {
	if len(args) == 0 {
		b.sendReply("⚠️ 请指定币种，例如: <code>/asset BTC</code>")
		return
	}

	asset := strings.ToUpper(strings.TrimSpace(args[0]))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔍 <b>%s 余额查询</b>\n", html.EscapeString(asset)))
	sb.WriteString(fmt.Sprintf("📅 %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	// 现货余额
	spotBal, spotErr := b.binance.GetSpotBalance(ctx, asset)
	if spotErr != nil {
		sb.WriteString(fmt.Sprintf("❌ 现货查询失败: %s\n\n", html.EscapeString(spotErr.Error())))
	} else {
		sb.WriteString("📊 <b>现货账户:</b>\n")
		sb.WriteString(fmt.Sprintf("   总计: %.8g\n", spotBal.Total))
		sb.WriteString(fmt.Sprintf("   可用: %.8g\n", spotBal.Free))
		if spotBal.Locked > 0 {
			sb.WriteString(fmt.Sprintf("   冻结: %.8g\n", spotBal.Locked))
		}
		sb.WriteString("\n")
	}

	// 活期理财持仓
	earnPositions, earnErr := b.binance.GetFlexiblePositions(ctx, asset)
	if earnErr != nil {
		sb.WriteString(fmt.Sprintf("❌ 活期理财查询失败: %s\n", html.EscapeString(earnErr.Error())))
	} else {
		sb.WriteString("🏦 <b>活期理财:</b>\n")
		if len(earnPositions) == 0 {
			sb.WriteString("   (无持仓)\n")
		}
		for _, pos := range earnPositions {
			redeemable := pos.GetRedeemableAmount()
			sb.WriteString(fmt.Sprintf("   总量: %s\n", pos.TotalAmount))
			sb.WriteString(fmt.Sprintf("   可赎回: %s\n", redeemable))
		}
	}

	b.sendReply(sb.String())
}

// handlePool 处理 /pool 命令 — 查询各交易对的 Pool 累积金额
func (b *Bot) handlePool() {
	var sb strings.Builder
	sb.WriteString("🏊 <b>Pool 累积金额</b>\n")
	sb.WriteString(fmt.Sprintf("📅 %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	if b.poolGetter == nil {
		sb.WriteString("⚠️ Pool 功能未初始化\n")
		b.sendReply(sb.String())
		return
	}

	pool := b.poolGetter()
	if len(pool) == 0 {
		sb.WriteString("(所有交易对 Pool 均为 0)\n")
		b.sendReply(sb.String())
		return
	}

	totalPool := 0.0
	hasNonZero := false
	for pair, amount := range pool {
		if amount > 0 {
			hasNonZero = true
		}
		totalPool += amount
		sb.WriteString(fmt.Sprintf("• <b>%s</b>: %.8f USDT\n", pair, amount))
	}

	if !hasNonZero {
		sb.WriteString("\n(所有交易对 Pool 均为 0)\n")
	} else {
		sb.WriteString(fmt.Sprintf("\n💰 Pool 合计: %.8f USDT\n", totalPool))
	}

	sb.WriteString("\n💡 <i>Pool 会在每次定投时自动叠加到定投金额上</i>")

	b.sendReply(sb.String())
}

// handleSetPool 处理 /setpool 命令 — 设置指定交易对的 Pool 金额
func (b *Bot) handleSetPool(args []string) {
	if b.poolSetter == nil {
		b.sendReply("⚠️ Pool 功能未初始化")
		return
	}

	if len(args) == 0 || len(args)%2 != 0 {
		b.sendReply("⚠️ 用法: <code>/setpool &lt;交易对&gt; &lt;金额&gt; [&lt;交易对2&gt; &lt;金额2&gt; ...]</code>\n例: <code>/setpool BTCUSDT 0.5</code>\n例: <code>/setpool BTCUSDT 0.5 ETHUSDT 0.3</code>")
		return
	}

	var sb strings.Builder
	sb.WriteString("🏊 <b>Pool 设置结果</b>\n\n")

	allOK := true
	for i := 0; i < len(args); i += 2 {
		pair := strings.ToUpper(strings.TrimSpace(args[i]))
		amountStr := strings.TrimSpace(args[i+1])

		amount, err := strconv.ParseFloat(amountStr, 64)
		if err != nil {
			sb.WriteString(fmt.Sprintf("❌ <b>%s</b>: 金额格式无效 (%s)\n", html.EscapeString(pair), html.EscapeString(amountStr)))
			allOK = false
			continue
		}
		if amount < 0 {
			sb.WriteString(fmt.Sprintf("❌ <b>%s</b>: 金额不能为负数\n", html.EscapeString(pair)))
			allOK = false
			continue
		}
		if err := b.poolSetter(pair, amount); err != nil {
			sb.WriteString(fmt.Sprintf("❌ <b>%s</b>: %s\n", html.EscapeString(pair), html.EscapeString(err.Error())))
			allOK = false
		} else {
			sb.WriteString(fmt.Sprintf("✅ <b>%s</b>: %.8f USDT\n", pair, amount))
		}
	}

	if b.envUpdater != nil {
		if err := b.envUpdater(); err != nil {
			sb.WriteString(fmt.Sprintf("\n⚠️ .env 文件更新失败: %s", html.EscapeString(err.Error())))
		} else if allOK {
			sb.WriteString("\n💾 已同步更新 .env 文件")
		}
	}

	b.sendReply(sb.String())
}

// handleHelp 处理 /help 命令
func (b *Bot) handleHelp() {
	text := `🤖 <b>币安定投机器人 — 命令列表</b>

/balance — 查询所有账户余额总览
/spot — 查询现货账户余额
/earn — 查询活期理财持仓
/asset &lt;币种&gt; — 查询指定币种余额
  例: <code>/asset BTC</code>
  例: <code>/asset USDT</code>
/pool — 查询各交易对的 Pool 累积金额
/setpool &lt;交易对&gt; &lt;金额&gt; — 设置 Pool 金额并同步 .env
  例: <code>/setpool BTCUSDT 0.5</code>
  例: <code>/setpool BTCUSDT 0.5 ETHUSDT 0.3</code>
/monitor — 查询价格监控状态
/monitor menu — 打开交易对设置菜单（增删/修改）
/help — 显示此帮助信息`

	b.sendReply(text)
}

// inlineButton 内联键盘按钮
type inlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
}

// handleMonitor 处理 /monitor 命令
//
//	/monitor                 查看状态 + 内联按钮
//	/monitor add BTCUSDT:1h&4h
//	/monitor del BTCUSDT
//	/monitor set BTCUSDT:1h&4h,ETHUSDT:15m
//	/monitor cancel
func (b *Bot) handleMonitor(args []string) {
	if b.monitorSymbolsGetter == nil || b.monitorSymbolsSetter == nil {
		b.sendReply("⚠️ 价格监控未启用或未配置")
		return
	}

	if len(args) == 0 {
		// 纯 /monitor 保持原有行为：状态 + 立即检查
		b.sendMonitorStatus()
		return
	}

	switch strings.ToLower(args[0]) {
	case "menu", "edit", "config":
		b.sendHTMLWithMarkup(b.monitorMenuText(), b.monitorKeyboard())
	case "add":
		if len(args) < 2 {
			b.promptAddSymbol()
			return
		}
		if err := b.addMonitorSpecs(splitSpecList(strings.Join(args[1:], " "))); err != nil {
			b.sendReply("❌ " + html.EscapeString(err.Error()))
			return
		}
		b.sendHTMLWithMarkup(b.monitorMenuText(), b.monitorKeyboard())
	case "del", "delete", "remove", "rm":
		if len(args) < 2 {
			b.sendReply("用法: <code>/monitor del BTCUSDT</code>")
			return
		}
		if err := b.removeMonitorSpecs(args[1:]); err != nil {
			b.sendReply("❌ " + html.EscapeString(err.Error()))
			return
		}
		b.sendHTMLWithMarkup(b.monitorMenuText(), b.monitorKeyboard())
	case "set":
		if len(args) < 2 {
			b.sendReply("用法: <code>/monitor set BTCUSDT:1h&4h,ETHUSDT:15m</code>")
			return
		}
		if err := b.monitorSymbolsSetter(splitSpecList(strings.Join(args[1:], " "))); err != nil {
			b.sendReply("❌ " + html.EscapeString(err.Error()))
			return
		}
		b.sendHTMLWithMarkup(b.monitorMenuText(), b.monitorKeyboard())
	case "list":
		b.sendHTMLWithMarkup(b.monitorMenuText(), b.monitorKeyboard())
	case "cancel":
		chatIDInt, _ := strconv.ParseInt(b.chatID, 10, 64)
		delete(b.pendingAdd, chatIDInt)
		b.sendReply("已取消添加")
	default:
		b.sendReply("用法: <code>/monitor</code> 打开菜单，或 <code>/monitor add BTCUSDT:1h&amp;4h</code>")
	}
}

// sendMonitorStatus 保持原有 /monitor 行为：状态 + 立即检查结果
func (b *Bot) sendMonitorStatus() {
	var sb strings.Builder
	sb.WriteString("📡 <b>价格监控状态</b>\n")
	sb.WriteString(fmt.Sprintf("📅 %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	if b.monitorGetter == nil {
		sb.WriteString("⚠️ 价格监控未启用\n")
		b.sendReply(sb.String())
		return
	}

	if status := b.monitorGetter(); status != "" {
		sb.WriteString(status)
		sb.WriteString("\n")
	}
	if b.monitorCheck != nil {
		sb.WriteString(b.monitorCheck())
	}

	b.sendReply(sb.String())
}

// monitorMenuText 生成监控状态 + 交易对列表
func (b *Bot) monitorMenuText() string {
	var sb strings.Builder
	sb.WriteString("📡 <b>价格监控</b>\n")
	sb.WriteString(fmt.Sprintf("📅 %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	if b.monitorGetter != nil {
		if status := b.monitorGetter(); status != "" {
			sb.WriteString(status)
			sb.WriteString("\n")
		}
	}

	sb.WriteString("<b>当前监控交易对</b>\n")
	specs := b.monitorSymbolsGetter()
	if len(specs) == 0 {
		sb.WriteString("（空）\n")
	}
	for i, spec := range specs {
		sb.WriteString(fmt.Sprintf("%d. <code>%s</code>\n", i+1, html.EscapeString(spec)))
	}
	sb.WriteString("\n点击下方按钮删除交易对，或点「添加」后直接回复。")
	return sb.String()
}

// monitorKeyboard 生成内联键盘
func (b *Bot) monitorKeyboard() interface{} {
	specs := b.monitorSymbolsGetter()
	rows := make([][]inlineButton, 0, len(specs)+1)
	for _, spec := range specs {
		rows = append(rows, []inlineButton{{
			Text:         "❌ " + spec,
			CallbackData: "mondel:" + symbolPart(spec),
		}})
	}
	rows = append(rows, []inlineButton{
		{Text: "➕ 添加交易对", CallbackData: "monadd"},
		{Text: "🔄 刷新", CallbackData: "monrefresh"},
	})
	return map[string]interface{}{"inline_keyboard": rows}
}

// handleCallbackQuery 处理内联按钮回调
func (b *Bot) handleCallbackQuery(q *CallbackQuery) {
	if q == nil || q.Message == nil {
		return
	}
	if fmt.Sprintf("%d", q.Message.Chat.ID) != b.chatID {
		log.Printf("⚠️ 忽略来自未授权聊天的回调: chatID=%d", q.Message.Chat.ID)
		return
	}

	switch {
	case q.Data == "monrefresh":
		b.answerCallback(q.ID, "已刷新")
		b.editHTMLWithMarkup(q.Message.MessageID, b.monitorMenuText(), b.monitorKeyboard())
	case q.Data == "monadd":
		b.answerCallback(q.ID, "请回复要添加的交易对")
		b.promptAddSymbol()
	case strings.HasPrefix(q.Data, "mondel:"):
		sym := strings.TrimPrefix(q.Data, "mondel:")
		if err := b.removeMonitorSpecs([]string{sym}); err != nil {
			b.answerCallback(q.ID, "❌ "+err.Error())
			return
		}
		b.answerCallback(q.ID, "已删除 "+sym)
		b.editHTMLWithMarkup(q.Message.MessageID, b.monitorMenuText(), b.monitorKeyboard())
	}
}

// promptAddSymbol 发送 ForceReply，等待用户输入交易对
func (b *Bot) promptAddSymbol() {
	chatIDInt, _ := strconv.ParseInt(b.chatID, 10, 64)
	b.pendingAdd[chatIDInt] = true

	markup := map[string]interface{}{
		"force_reply":             true,
		"selective":               true,
		"input_field_placeholder": "例如 BTCUSDT:1h&4h",
	}
	text := "➕ <b>添加监控交易对</b>\n请直接回复，格式：<code>BTCUSDT:1h&amp;4h</code>\n多个用逗号分隔。发送「取消」放弃。"
	b.sendHTMLWithMarkup(text, markup)
}

// handlePendingAdd 处理 ForceReply 返回的文本
func (b *Bot) handlePendingAdd(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if text == "取消" || strings.EqualFold(text, "cancel") {
		b.sendReply("已取消添加")
		return
	}

	if err := b.addMonitorSpecs(splitSpecList(text)); err != nil {
		b.sendReply("❌ " + html.EscapeString(err.Error()))
		return
	}
	b.sendHTMLWithMarkup(b.monitorMenuText(), b.monitorKeyboard())
}

// addMonitorSpecs 追加/覆盖交易对（按 symbol 去重）
func (b *Bot) addMonitorSpecs(newSpecs []string) error {
	if len(newSpecs) == 0 {
		return fmt.Errorf("没有可添加的交易对")
	}

	cur := b.monitorSymbolsGetter()
	index := make(map[string]int, len(cur))
	for i, spec := range cur {
		index[symbolPart(spec)] = i
	}

	added := 0
	for _, spec := range newSpecs {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		sym := symbolPart(spec)
		if sym == "" {
			continue
		}
		if i, ok := index[sym]; ok {
			cur[i] = spec
		} else {
			cur = append(cur, spec)
			index[sym] = len(cur) - 1
		}
		added++
	}
	if added == 0 {
		return fmt.Errorf("没有可添加的交易对")
	}
	return b.monitorSymbolsSetter(cur)
}

// removeMonitorSpecs 按 symbol 删除交易对
func (b *Bot) removeMonitorSpecs(symbols []string) error {
	drop := make(map[string]bool, len(symbols))
	for _, s := range symbols {
		drop[symbolPart(s)] = true
	}

	cur := b.monitorSymbolsGetter()
	out := make([]string, 0, len(cur))
	for _, spec := range cur {
		if !drop[symbolPart(spec)] {
			out = append(out, spec)
		}
	}
	if len(out) == len(cur) {
		return fmt.Errorf("未找到要删除的交易对")
	}
	if len(out) == 0 {
		return fmt.Errorf("至少保留一个监控交易对")
	}
	return b.monitorSymbolsSetter(out)
}

// splitSpecList 拆分逗号/空白分隔的交易对列表
func splitSpecList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// symbolPart 取 "BTCUSDT:1h&4h" 中的 "BTCUSDT"
func symbolPart(spec string) string {
	if idx := strings.Index(spec, ":"); idx >= 0 {
		spec = spec[:idx]
	}
	return strings.ToUpper(strings.TrimSpace(spec))
}

// sendHTMLWithMarkup 发送 HTML 消息，可带 reply_markup
func (b *Bot) sendHTMLWithMarkup(text string, markup interface{}) {
	payload := map[string]interface{}{
		"chat_id":                  b.chatID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if markup != nil {
		payload["reply_markup"] = markup
	}
	if err := b.callAPI("sendMessage", payload); err != nil {
		log.Printf("❌ 发送消息失败: %v", err)
	}
}

// editHTMLWithMarkup 编辑已发送的 HTML 消息
func (b *Bot) editHTMLWithMarkup(messageID int, text string, markup interface{}) {
	payload := map[string]interface{}{
		"chat_id":                  b.chatID,
		"message_id":               messageID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if markup != nil {
		payload["reply_markup"] = markup
	}
	if err := b.callAPI("editMessageText", payload); err != nil {
		log.Printf("⚠️ 编辑消息失败: %v", err)
	}
}

// answerCallback 回应按钮回调（避免客户端转圈）
func (b *Bot) answerCallback(callbackID, text string) {
	payload := map[string]interface{}{"callback_query_id": callbackID}
	if text != "" {
		payload["text"] = text
	}
	if err := b.callAPI("answerCallbackQuery", payload); err != nil {
		log.Printf("⚠️ 回应回调失败: %v", err)
	}
}

// callAPI 调用 Telegram Bot API
func (b *Bot) callAPI(method string, payload map[string]interface{}) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", b.botToken, method)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := b.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API %s 错误 %d: %s", method, resp.StatusCode, string(respBody))
	}
	return nil
}

// boolToEmoji 布尔值转 emoji
func boolToEmoji(b bool) string {
	if b {
		return "✅ 是"
	}
	return "❌ 否"
}
