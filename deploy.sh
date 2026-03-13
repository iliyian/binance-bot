#!/bin/bash
set -e

# ================================================
# 币安自动定投机器人 - 一键部署脚本 (Docker Compose)
# ================================================

CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${CYAN}"
echo "╔══════════════════════════════════════════╗"
echo "║   🤖 币安自动定投机器人 - 一键部署       ║"
echo "╚══════════════════════════════════════════╝"
echo -e "${NC}"

# ---- 检查依赖 ----
check_dependency() {
    if ! command -v "$1" &> /dev/null; then
        echo -e "${RED}❌ 未找到 $1，请先安装${NC}"
        exit 1
    fi
    echo -e "${GREEN}✅ $1 已安装${NC}"
}

echo -e "${YELLOW}📋 检查环境依赖...${NC}"
check_dependency "docker"

# 检查 docker compose (v2) 或 docker-compose (v1)
if docker compose version &> /dev/null; then
    COMPOSE_CMD="docker compose"
    echo -e "${GREEN}✅ docker compose 已安装${NC}"
elif command -v docker-compose &> /dev/null; then
    COMPOSE_CMD="docker-compose"
    echo -e "${GREEN}✅ docker-compose 已安装${NC}"
else
    echo -e "${RED}❌ 未找到 docker compose，请先安装${NC}"
    exit 1
fi

# ---- 检查 .env 文件 ----
echo ""
if [ ! -f .env ]; then
    echo -e "${YELLOW}⚠️  未找到 .env 文件，正在从模板创建...${NC}"
    cp .env.example .env
    echo -e "${YELLOW}📝 请编辑 .env 文件填写您的配置:${NC}"
    echo -e "   ${CYAN}nano .env${NC}  或  ${CYAN}vim .env${NC}"
    echo ""
    echo -e "${RED}⚠️  请填写以下必要配置后重新运行部署脚本:${NC}"
    echo "   - BINANCE_API_KEY"
    echo "   - BINANCE_SECRET_KEY"
    echo "   - TRADE_PAIRS"
    echo "   - TRADE_AMOUNTS"
    echo "   - TELEGRAM_BOT_TOKEN (可选)"
    echo "   - TELEGRAM_CHAT_ID (可选)"
    exit 0
fi

echo -e "${GREEN}✅ .env 文件已存在${NC}"

# ---- 验证必要配置 ----
echo -e "\n${YELLOW}🔍 验证配置...${NC}"

source .env 2>/dev/null || true

missing=0
for var in BINANCE_API_KEY BINANCE_SECRET_KEY TRADE_PAIRS TRADE_AMOUNTS; do
    val=$(grep "^${var}=" .env | cut -d'=' -f2-)
    if [ -z "$val" ] || [[ "$val" == *"your_"* ]]; then
        echo -e "${RED}❌ ${var} 未配置或为默认值${NC}"
        missing=1
    fi
done

if [ "$missing" -eq 1 ]; then
    echo -e "\n${RED}请编辑 .env 文件完成配置后重新运行${NC}"
    exit 1
fi

echo -e "${GREEN}✅ 配置验证通过${NC}"

# ---- Docker Compose 部署 ----
echo -e "\n${CYAN}🐳 使用 Docker Compose 部署...${NC}"

# 预创建日志目录，避免 Docker 以 root 创建导致权限问题
mkdir -p logs

# 停止旧容器
$COMPOSE_CMD down 2>/dev/null || true

# 拉取最新镜像并启动
$COMPOSE_CMD up -d --pull always

echo -e "\n${GREEN}✅ 部署成功！${NC}"
echo -e "查看日志: ${CYAN}$COMPOSE_CMD logs -f${NC}"
echo -e "停止服务: ${CYAN}$COMPOSE_CMD down${NC}"
echo -e "重启服务: ${CYAN}$COMPOSE_CMD restart${NC}"

echo ""
echo -e "${GREEN}🎉 部署完成！${NC}"
echo -e "${CYAN}如需帮助，请查看 README.md${NC}"
