#!/bin/sh
# Установка balanda на Linux и macOS:
#
#   curl -fsSL https://raw.githubusercontent.com/Ar4ikov/balanda/main/install.sh | sh
#
# Переменные окружения:
#   BALANDA_VERSION      версия, например v0.1.0; по умолчанию последний релиз
#   BALANDA_INSTALL_DIR  куда положить бинарник; по умолчанию ~/.local/bin,
#                        а под root — /usr/local/bin
#   BALANDA_LOCAL_DIR    взять архив и checksums.txt из этого каталога, а не
#                        с GitHub (так CI проверяет скрипт на snapshot-сборке)
set -eu

REPO="Ar4ikov/balanda"

say() {
	printf '%s\n' "$*"
}

die() {
	printf 'balanda: %s\n' "$*" >&2
	exit 1
}

detect_os() {
	case "$(uname -s)" in
	Linux) echo linux ;;
	Darwin) echo darwin ;;
	*) die "ОС $(uname -s) не поддерживается; на Windows ставьте через install.ps1" ;;
	esac
}

detect_arch() {
	arch=$(uname -m)
	# Терминал под Rosetta на Apple Silicon видит x86_64, но родной
	# бинарник arm64 там тоже запустится.
	if [ "$(uname -s)" = Darwin ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
		arch=arm64
	fi
	case "$arch" in
	x86_64 | amd64) echo amd64 ;;
	aarch64 | arm64) echo arm64 ;;
	*) die "архитектура $arch не поддерживается, есть сборки для amd64 и arm64" ;;
	esac
}

download() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		die "нужен curl или wget"
	fi
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | awk '{print $NF}'
	else
		die "нечем посчитать sha256: нужен sha256sum, shasum или openssl"
	fi
}

os=$(detect_os)
arch=$(detect_arch)
archive="balanda_${os}_${arch}.tar.gz"

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t balanda)
trap 'rm -rf "$tmp"' EXIT INT TERM

if [ -n "${BALANDA_LOCAL_DIR:-}" ]; then
	say "Беру $archive из $BALANDA_LOCAL_DIR"
	[ -f "$BALANDA_LOCAL_DIR/$archive" ] || die "нет файла $BALANDA_LOCAL_DIR/$archive"
	cp "$BALANDA_LOCAL_DIR/$archive" "$BALANDA_LOCAL_DIR/checksums.txt" "$tmp/" ||
		die "не скопировать файлы из $BALANDA_LOCAL_DIR"
else
	if [ -n "${BALANDA_VERSION:-}" ]; then
		version=$BALANDA_VERSION
		case "$version" in v*) ;; *) version="v$version" ;; esac
		base="https://github.com/$REPO/releases/download/$version"
	else
		base="https://github.com/$REPO/releases/latest/download"
	fi
	say "Скачиваю $base/$archive"
	download "$base/$archive" "$tmp/$archive" || die "не скачать $archive; есть ли такой релиз?"
	download "$base/checksums.txt" "$tmp/checksums.txt" || die "не скачать checksums.txt"
fi

expected=$(awk -v f="$archive" '$2 == f || $2 == "*" f {print $1}' "$tmp/checksums.txt")
[ -n "$expected" ] || die "в checksums.txt нет строки для $archive"
actual=$(sha256 "$tmp/$archive")
[ "$expected" = "$actual" ] || die "контрольная сумма $archive не сошлась: ждали $expected, получили $actual"
say "sha256 сошлась"

tar -xzf "$tmp/$archive" -C "$tmp" balanda || die "в архиве нет бинарника balanda"

if [ -n "${BALANDA_INSTALL_DIR:-}" ]; then
	dir=$BALANDA_INSTALL_DIR
elif [ "$(id -u)" = 0 ]; then
	dir=/usr/local/bin
else
	dir="$HOME/.local/bin"
fi
mkdir -p "$dir" || die "не создать $dir"
cp "$tmp/balanda" "$dir/balanda.new" || die "нет прав на запись в $dir; задайте BALANDA_INSTALL_DIR"
chmod 0755 "$dir/balanda.new"
mv -f "$dir/balanda.new" "$dir/balanda"

say "Готово: $("$dir/balanda" version --short) в $dir/balanda"
case ":$PATH:" in
*":$dir:"*)
	say "Проверьте: balanda travit"
	;;
*)
	say ""
	say "Каталога $dir нет в PATH. Добавьте строку в ~/.profile, ~/.bashrc или ~/.zshrc:"
	say "  export PATH=\"$dir:\$PATH\""
	say "и откройте новый терминал."
	;;
esac
