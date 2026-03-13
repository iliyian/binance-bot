#!/bin/bash
set -e

# ================================================
# 币安自动定投机器人 - 一键部署脚本
# ================================================

CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

PROJECT_NAME="binance-bot"

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
check_dependency "docker-compose" || check_dependency "docker" # docker compose v2

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

# ---- 选择部署方式 ----
echo ""
echo -e "${YELLOW}请选择部署方式:${NC}"
echo "  1) Docker Compose (推荐)"
echo "  2) Docker 单容器"
echo "  3) 直接编译运行 (需要 Go 环境)"
echo ""
read -p "请输入选项 [1]: " choice
choice=${choice:-1}

case $choice in
    1)
        echo -e "\n${CYAN}🐳 使用 Docker Compose 部署...${NC}"
        
        # 停止旧容器
        docker-compose down 2>/dev/null || true
        
        # 构建并启动
        docker-compose up -d --build
        
        echo -e "\n${GREEN}✅ 部署成功！${NC}"
        echo -e "查看日志: ${CYAN}docker-compose logs -f${NC}"
        echo -e "停止服务: ${CYAN}docker-compose down${NC}"
        echo -e "重启服务: ${CYAN}docker-compose restart${NC}"
        ;;
    2)
        echo -e "\n${CYAN}🐳 使用 Docker 部署...${NC}"
        
        # 停止并删除旧容器
        docker stop $PROJECT_NAME 2>/dev/null || true
        docker rm $PROJECT_NAME 2>/dev/null || true
        
        # 构建镜像
        docker build -t $PROJECT_NAME .
        
        # 运行容器
        docker run -d \
            --name $PROJECT_NAME \
            --restart unless-stopped \
            --env-file .env \
            $PROJECT_NAME
        
        echo -e "\n${GREEN}✅ 部署成功！${NC}"
        echo -e "查看日志: ${CYAN}docker logs -f $PROJECT_NAME${NC}"
        echo -e "停止服务: ${CYAN}docker stop $PROJECT_NAME${NC}"
        echo -e "重启服务: ${CYAN}docker restart $PROJECT_NAME${NC}"
        ;;
    3)
        echo -e "\n${CYAN}🔨 直接编译运行...${NC}"
        
        if ! command -v go &> /dev/null; then
            echo -e "${RED}❌ 未找到 Go 环境，请先安装 Go 1.21+${NC}"
            exit 1
        fi
        
        echo "正在下载依赖..."
        go mod tidy
        
        echo "正在编译..."
        CGO_ENABLED=0 go build -ldflags="-s -w" -o $PROJECT_NAME .
        
        echo -e "${GREEN}✅ 编译成功！${NC}"
        echo -e "运行定投: ${CYAN}./$PROJECT_NAME${NC}"
        echo -e "单次执行: ${CYAN}./$PROJECT_NAME -once${NC}"
        echo ""
        
        # 询问是否创建 systemd 服务
        read -p "是否创建 systemd 服务以后台运行？[y/N]: " create_service
        if [[ "$create_service" =~ ^[Yy]$ ]]; then
            WORK_DIR=$(pwd)
            SERVICE_FILE="/etc/systemd/system/${PROJECT_NAME}.service"
            
            sudo tee $SERVICE_FILE > /dev/null << EOF
[Unit]
Description=Binance Auto DCA Bot
After=network.target

[Service]
Type=simple
User=$(whoami)
WorkingDirectory=${WORK_DIR}
ExecStart=${WORK_DIR}/${PROJECT_NAME}
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
            
            sudo systemctl daemon-reload
            sudo systemctl enable $PROJECT_NAME
            sudo systemctl start $PROJECT_NAME
            
            echo -e "\n${GREEN}✅ Systemd 服务已创建并启动！${NC}"
            echo -e "查看状态: ${CYAN}sudo systemctl status $PROJECT_NAME${NC}"
            echo -e "查看日志: ${CYAN}sudo journalctl -u $PROJECT_NAME -f${NC}"
            echo -e "停止服务: ${CYAN}sudo systemctl stop $PROJECT_NAME${NC}"
            echo -e "重启服务: ${CYAN}sudo systemctl restart $PROJECT_NAME${NC}"
        fi
        ;;
    *)
        echo -e "${RED}❌ 无效选项${NC}"
        exit 1
        ;;
esac

echo ""
echo -e "${GREEN}🎉 部署完成！${NC}"
echo -e "${CYAN}如需帮助，请查看 README.md${NC}"
