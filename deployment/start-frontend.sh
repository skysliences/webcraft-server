#!/bin/sh
set -eu
server_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$server_root"
if [ ! -f server.env ]; then echo '请先配置 server.env。' >&2; exit 1; fi
set -a
. ./server.env
set +a
exec node "$server_root/frontend-runtime/server.mjs"
