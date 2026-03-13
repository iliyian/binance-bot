# ===== 构建阶段 =====
FROM --platform=$BUILDPLATFORM golang:1.21-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# 先复制依赖文件，利用 Docker 缓存
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并构建
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o binance-bot .

# ===== 运行阶段 =====
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

# 创建非 root 用户
RUN adduser -D -g '' appuser

WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /app/binance-bot .

# 使用非 root 用户
USER appuser

ENTRYPOINT ["./binance-bot"]
