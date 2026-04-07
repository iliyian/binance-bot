# ===== 构建阶段 =====
FROM --platform=$BUILDPLATFORM golang:1.21-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG COMMIT_HASH=dev

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# 先复制依赖文件，利用 Docker 缓存
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并构建
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags="-s -w -X main.commitHash=${COMMIT_HASH}" -o binance-bot .

# ===== 运行阶段 =====
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata su-exec

# 创建非 root 用户
RUN adduser -D -g '' appuser

WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /app/binance-bot .

# 复制 entrypoint 脚本
COPY entrypoint.sh .
RUN chmod +x entrypoint.sh

# 创建日志目录并赋予 appuser 权限
RUN mkdir -p /app/logs && chown appuser:appuser /app/logs

ENTRYPOINT ["./entrypoint.sh"]
CMD ["./binance-bot"]
