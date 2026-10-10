#!/bin/sh
set -eu
umask 077
account_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$account_root"
if [ ! -f account.env ]; then
  echo '请复制 account.env.example 为 account.env，填写账号服务域名和游戏首页地址。' >&2
  exit 1
fi
set -a
. ./account.env
set +a
if [ -z "${IDENTITY_PUBLIC_URL:-}" ] || [ -z "${IDENTITY_GAME_HOME_URL:-}" ]; then
  echo '需要填写 IDENTITY_PUBLIC_URL 和 IDENTITY_GAME_HOME_URL。' >&2
  exit 1
fi
case "$IDENTITY_PUBLIC_URL $IDENTITY_GAME_HOME_URL" in
  *example.com*) echo '请将示例账号服务和游戏首页域名替换为实际地址。' >&2; exit 1;;
esac
case "$IDENTITY_PUBLIC_URL" in
  https://*)
    if [ "${AUTH_COOKIE_SECURE:-true}" != true ]; then
      echo '公开 HTTPS 账号服务需要 AUTH_COOKIE_SECURE=true。' >&2
      exit 1
    fi
    ;;
esac
export PROJECT_ROOT="$account_root"
export IDENTITY_HOST="${IDENTITY_HOST:-127.0.0.1}"
export IDENTITY_PORT="${IDENTITY_PORT:-8082}"
export IDENTITY_DATA_DIR="${IDENTITY_DATA_DIR:-$account_root/data}"
export IDENTITY_PLUGIN_DIR="${IDENTITY_PLUGIN_DIR:-$account_root/account-plugins}"
export AUTH_COOKIE_SECURE="${AUTH_COOKIE_SECURE:-true}"
export IDENTITY_ALLOW_LEGACY_COOKIE="${IDENTITY_ALLOW_LEGACY_COOKIE:-false}"
mkdir -p "$IDENTITY_DATA_DIR" "$IDENTITY_PLUGIN_DIR"
exec "$account_root/bin/mcraft-account"
