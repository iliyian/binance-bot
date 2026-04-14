#!/bin/sh
# 修复挂载卷的权限（以 root 运行时）
if [ "$(id -u)" = "0" ]; then
    chown appuser:appuser /app/logs 2>/dev/null || true
    chown appuser:appuser /app/.env 2>/dev/null || true
    exec su-exec appuser "$@"
else
    exec "$@"
fi
