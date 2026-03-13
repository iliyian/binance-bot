# 🤖 币安自动定投机器人

基于 Golang 编写的币安 (Binance) 自动定投脚本，支持多交易对定时市价买入，并通过 Telegram 发送交易通知。

## ✨ 功能特性

- 📊 **多交易对定投** — 支持同时定投多个币种 (BTC、ETH、SOL 等)
- ⏰ **灵活定时** — 基于 cron 表达式配置执行时间，支持秒级精度
- 📱 **Telegram 通知** — 交易完成后自动推送详细报告
- 🐳 **Docker 部署** — 支持 Docker / Docker Compose 一键部署
- 🔒 **安全运行** — 容器内非 root 用户运行，配置与代码分离
- 🧪 **测试网支持** — 可切换币安测试网进行功能验证

## 📁 项目结构

```
binance-bot/
├── main.go                 # 程序入口
├── config/
│   └── config.go           # 配置加载
├── binance/
│   └── client.go           # 币安 API 交易客户端
├── telegram/
│   └── notify.go           # Telegram 通知
├── scheduler/
│   └── scheduler.go        # 定时调度器
├── .env.example            # 配置模板
├── Dockerfile              # Docker 构建文件
├── docker-compose.yml      # Docker Compose 编排
├── deploy.sh               # 一键部署脚本
├── go.mod                  # Go 模块定义
└── README.md               # 本文档
```

## 🚀 快速开始

### 前置条件

- [币安 API Key](https://www.binance.com/zh-CN/my/settings/api-management) (需开启现货交易权限)
- [Telegram Bot Token](https://core.telegram.org/bots#creating-a-new-bot) (可选，用于接收通知)
- Docker & Docker Compose 或 Go 1.21+

### 一键部署

```bash
# 1. 克隆项目
git clone https://github.com/iliyian/binance-bot.git
cd binance-bot

# 2. 运行部署脚本
chmod +x deploy.sh
./deploy.sh
```

部署脚本会引导你完成配置和选择部署方式（Docker Compose / Docker / 直接编译）。

### 手动部署

#### 方式一：Docker Compose（推荐）

```bash
# 复制并编辑配置
cp .env.example .env
nano .env

# 启动
docker-compose up -d --build

# 查看日志
docker-compose logs -f
```

#### 方式二：直接编译运行

```bash
# 复制并编辑配置
cp .env.example .env
nano .env

# 编译
go mod tidy
go build -o binance-bot .

# 运行（定时模式）
./binance-bot

# 单次执行（测试用）
./binance-bot -once
```

## ⚙️ 配置说明

编辑 `.env` 文件进行配置：

| 配置项 | 必填 | 说明 | 示例 |
|--------|------|------|------|
| `BINANCE_API_KEY` | ✅ | 币安 API Key | `abc123...` |
| `BINANCE_SECRET_KEY` | ✅ | 币安 API Secret | `def456...` |
| `TRADE_PAIRS` | ✅ | 交易对，逗号分隔 | `BTCUSDT,ETHUSDT` |
| `TRADE_AMOUNTS` | ✅ | 每个交易对金额(USDT) | `10,5` |
| `CRON_SCHEDULE` | ❌ | 定时规则 (6位cron) | `0 0 9 * * *` |
| `TIMEZONE` | ❌ | 时区 | `Asia/Shanghai` |
| `TELEGRAM_BOT_TOKEN` | ❌ | Telegram Bot Token | `123456:ABC...` |
| `TELEGRAM_CHAT_ID` | ❌ | Telegram Chat ID | `-100123456` |
| `USE_TESTNET` | ❌ | 使用测试网 | `true` / `false` |
| `LOG_LEVEL` | ❌ | 日志级别 | `info` |

### Cron 表达式示例

格式：`秒 分 时 日 月 周`

| 表达式 | 说明 |
|--------|------|
| `0 0 9 * * *` | 每天 09:00 |
| `0 0 9,21 * * *` | 每天 09:00 和 21:00 |
| `0 30 8 * * 1-5` | 工作日 08:30 |
| `0 0 */4 * * *` | 每 4 小时 |
| `0 0 0 1 * *` | 每月 1 日 00:00 |

## 📱 Telegram 通知设置

1. 在 Telegram 中找到 [@BotFather](https://t.me/BotFather)，发送 `/newbot` 创建机器人
2. 获取 Bot Token（格式如 `123456:ABC-DEF`）
3. 获取 Chat ID：
   - 向你的 bot 发送一条消息
   - 访问 `https://api.telegram.org/bot<TOKEN>/getUpdates`
   - 找到 `chat.id` 字段
4. 将 Token 和 Chat ID 填入 `.env`

### 通知示例

```
🤖 币安自动定投报告
📅 2024-01-15 09:00:03

✅ BTCUSDT — 成功
   💰 花费: 10.00 USDT
   📦 获得: 0.00023400
   📈 均价: 42735.04273504
   🔖 订单ID: 123456789

✅ ETHUSDT — 成功
   💰 花费: 5.00 USDT
   📦 获得: 0.00198000
   📈 均价: 2525.25252525
   🔖 订单ID: 987654321

━━━━━━━━━━━━━━━━━━
📊 成功: 2 | 失败: 0
💳 USDT 余额: 985.00 (可用: 985.00, 冻结: 0.00)
```

## 🔧 运维命令

```bash
# Docker Compose
docker-compose logs -f          # 查看实时日志
docker-compose restart           # 重启服务
docker-compose down              # 停止服务
docker-compose up -d --build     # 重新构建并启动

# Systemd (直接编译部署)
sudo systemctl status binance-bot    # 查看状态
sudo systemctl restart binance-bot   # 重启
sudo journalctl -u binance-bot -f    # 查看日志
```

## ⚠️ 注意事项

1. **API 权限**：只需开启「现货交易」权限，无需开启提现权限
2. **IP 白名单**：建议在币安 API 管理中设置服务器 IP 白名单
3. **最小金额**：币安市价单最小金额通常为 **5 USDT**，部分交易对可能更高
4. **资金安全**：确保 `.env` 文件权限为 `600`，不要提交到 Git 仓库
5. **测试验证**：首次使用建议先开启 `USE_TESTNET=true` 进行测试

## 📄 License

MIT License
