# Окружение для записи GIF. Tape-файлы подключают его так:
#   . docs/tapes/demo.sh
# Создаёт /tmp/balanda-demo с «мусорными» проектами и отдельным
# каталогом состояния для камер. Большие файлы разреженные: место на
# диске они не занимают, а размер у них настоящий.
export PS1='$ '
export BALANDA_STATE_DIR=/tmp/balanda-demo/state
rm -rf /tmp/balanda-demo
d=/tmp/balanda-demo/proekty
mkdir -p "$BALANDA_STATE_DIR" "$d/web/node_modules/react" "$d/web/.next/cache" \
	"$d/api/target/debug" "$d/ml/.venv/lib" "$d/ml/src/__pycache__"
echo '{}' >"$d/web/package.json"
truncate -s 186M "$d/web/node_modules/react/index.js"
truncate -s 42M "$d/web/.next/cache/webpack.pack"
echo '[package]' >"$d/api/Cargo.toml"
truncate -s 1300M "$d/api/target/debug/api"
echo 'home = /usr/bin' >"$d/ml/.venv/pyvenv.cfg"
truncate -s 640M "$d/ml/.venv/lib/torch.so"
truncate -s 3M "$d/ml/src/__pycache__/model.cpython-312.pyc"
(
	cd "$d" && git init -q -b main &&
		git -c user.name=demo -c user.email=demo@example.com commit -q --allow-empty -m init &&
		git branch fix/login && git branch feature/old-api
)
cd /tmp/balanda-demo || return
clear
