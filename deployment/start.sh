#!/bin/sh
set -eu
server_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$server_root"
if [ ! -f server.env ]; then
  echo '请复制 server.env.example 为 server.env，并填写核心账号及网站配置。' >&2
  exit 1
fi
set -a
. ./server.env
set +a
if [ "${ACCOUNT_MODE:-central}" != legacy ]; then
  AUTH_ISSUER_URL="${AUTH_ISSUER_URL:-https://login.ccraft.app}"
  export AUTH_ISSUER_URL
  if [ -z "${AUTH_ISSUER_URL:-}" ] || [ -z "${AUTH_SERVER_ID:-}" ] || [ -z "${AUTH_PUBLIC_KEY:-}" ] || [ -z "${AUTH_CALLBACK_URL:-}" ] || [ -z "${GAME_PUBLIC_URL:-}" ]; then
    echo '核心账号配置不完整：需要 issuer、公钥、本服注册 ID、回调 URL 和游戏网页 URL。' >&2
    exit 1
  fi
  case "$AUTH_ISSUER_URL $GAME_PUBLIC_URL" in *example.com*) echo '请替换示例域名。' >&2; exit 1;; esac
elif [ -n "${AUTH_ISSUER_URL:-}" ]; then
  echo 'legacy 测试模式需要留空 AUTH_ISSUER_URL。' >&2
  exit 1
fi
export PROJECT_ROOT="$server_root"
export DATA_DIR="${DATA_DIR:-$server_root/data}"
export ENGINE_FILE="$server_root/engine.wasm"
export PLUGIN_DIR="${PLUGIN_DIR:-$server_root/plugins}"
exec "$server_root/bin/mcraft"
