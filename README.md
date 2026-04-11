# 🤖 币安自动定投机器人

基于 Golang 编写的币安 (Binance) 自动定投脚本，支持多交易对定时市价买入，并通过 Telegram 发送交易通知。

> 💡 **为什么不用币安闪兑定投？**
>
> 币安闪兑（Convert）定投的汇率非常差，实测至少相当于 **0.6% 的隐含手续费**，远高于现货市价单的手续费（0.1% 甚至更低）。本项目直接通过现货市价单进行定投，确保以最优市场价格成交，大幅降低交易成本。

## ✨ 功能特性

- 📊 **通用量化框架** — 底层完全重构，支持任意自定义策略 (BUY/SELL)
- ⏰ **多时间级 K 线存储** — 内置纯 Go 实现的 SQLite，支持 1s/1m/1d 级别海量 K 线数据持久化
- 🔬 **毫秒级离线回测** — 支持指定历史数据区间、自定义资金的回测模式，生成期末绝对收益报告
- 📡 **WebSocket 实盘数据流** — 实盘毫秒级行情订阅，突破 API 限制，零延迟信号触发
- 🏊 **Pool 差额定投策略** — 默认内置 cron+pool 定投策略，不丢失任何精度差额资金
- 📱 **Telegram 通知** — 交易完成后自动推送详细报告（含 Pool 状态）
- 🐳 **Docker 部署** — 支持 Docker / Docker Compose 一键部署

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

# 回填历史数据 (下载过去 365 天的日 K 线)
./binance-bot -backfill -interval=1d -days=365

# 运行离线回测 (使用本地 SQLite 数据回测过去 365 天)
./binance-bot -backtest -interval=1d -days=365 -initial=10000
```

## ⚙️ 配置说明

编辑 `.env` 文件进行配置：

| 配置项 | 必填 | 说明 | 示例 |
|--------|------|------|------|
| `BINANCE_API_KEY` | ✅ | 币安 API Key | `abc123...` |
| `BINANCE_SECRET_KEY` | ✅ | 币安 API Secret | `def456...` |
| `TRADE_PAIRS` | ✅ | 交易对，逗号分隔 | `BTCUSDT,ETHUSDT` |
| `TRADE_AMOUNTS` | ✅ | 每个交易对金额(USDT) | `10,5` |
| `POOL_AMOUNTS` | ❌ | 初始Pool金额(USDT)，重启恢复用 | `0.5,0.3` |
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
   💰 花费: 9.99 USDT
   📦 获得: 0.00023400
   📈 均价: 42735.04273504
   🔖 订单ID: 123456789
   🏊 Pool: 0.00000000 → 0.01000000 USDT
   🎯 目标: 10.00000000, 实际: 9.99000000, 差额: 0.01000000

✅ ETHUSDT — 成功
   💰 花费: 5.00 USDT
   📦 获得: 0.00198000
   📈 均价: 2525.25252525
   🔖 订单ID: 987654321

━━━━━━━━━━━━━━━━━━
📊 成功: 2 | 失败: 0
💳 USDT 余额: 985.00 (可用: 985.00, 冻结: 0.00)

🏊 Pool 累积:
   • BTCUSDT: 0.01000000 USDT
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

## 🏊 Pool 差额累积

由于币安存在最小交易金额限制，每次定投实际扣除的金额可能略小于设定金额。Pool 功能会自动累积这些差额：

1. **自动累积**：每次定投后，`差额 = 目标金额 - 实际花费`，差额自动存入该交易对的 Pool
2. **自动叠加**：下次定投时，Pool 中的金额会加到配置金额上，确保长期投入不丢失任何资金
3. **重启恢复**：Pool 存储在内存中，重启后需通过 `POOL_AMOUNTS` 环境变量恢复（查看最近一次 Telegram 通知中的 Pool 值）
4. **Telegram 通知**：每次定投报告和启动通知中都会包含 Pool 状态

**示例**：配置定投 10 USDT 买 BTC，但币安最小精度导致实际花费 9.99 USDT，则 0.01 USDT 进入 Pool。下次定投实际金额为 10.01 USDT。

## ⚠️ 注意事项

1. **API 权限**：只需开启「现货交易」权限，无需开启提现权限
2. **IP 白名单**：建议在币安 API 管理中设置服务器 IP 白名单
3. **最小金额**：币安市价单最小金额通常为 **5 USDT**，部分交易对可能更高
4. **资金安全**：确保 `.env` 文件权限为 `600`，不要提交到 Git 仓库
5. **测试验证**：首次使用建议先开启 `USE_TESTNET=true` 进行测试
6. **Pool 恢复**：重启服务后，记得从最近一次 Telegram 通知中获取 Pool 值并设置 `POOL_AMOUNTS`

## 📄 License

MIT License
