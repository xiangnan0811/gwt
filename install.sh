#!/bin/sh
# gwt installer —— 下载对应架构的预编译二进制，校验 sha256 后安装。
#
# 设计要点：
#   * POSIX sh（dash / busybox ash 都能跑），只用 curl|wget + coreutils 级别的工具
#   * 永远校验 sha256；拿不到 SHA256SUMS 就拒绝安装（不给"跳过校验"的后门）
#   * 默认装到 $HOME/.local/bin，不需要 root；--prefix /usr/local 才需要 sudo
#   * 非 Linux 明确拒绝：gwt 用 Linux ioctl 判断终端与宽度
#
# 用法见 --help。

set -eu

REPO="xiangnan0811/gwt"
PROG="gwt"
BASE_URL="https://github.com/${REPO}/releases/download"

say()  { printf '%s\n' "$*"; }
warn() { printf '%s\n' "$*" >&2; }
die()  { printf '%s: 错误: %s\n' "$PROG-install" "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

usage() {
	cat <<'EOF'
用法: install.sh [选项]

下载并安装 gwt —— 只读的多仓库 git worktree 巡检工具。

选项:
  --version VER    指定版本，如 0.3.0 或 v0.3.0；默认 latest（最新正式发布）
  --prefix DIR     安装前缀，二进制装到 DIR/bin（默认 $HOME/.local）
  --dist-dir DIR   离线模式：从本地目录读取 gwt-linux-<arch> 与 SHA256SUMS
  --uninstall      卸载 $PREFIX/bin/gwt
  --dry-run        只打印将要做什么，不落地
  -h, --help       显示本帮助

环境变量:
  GWT_DIST_DIR     等价于 --dist-dir
  TMPDIR           临时目录位置

示例:
  curl -fsSL https://raw.githubusercontent.com/xiangnan0811/gwt/master/install.sh | sh
  curl -fsSL .../install.sh | sh -s -- --version 0.3.0 --prefix "$HOME/.local"
  sh install.sh --uninstall
EOF
}

# ---------------------------------------------------------------- 参数解析
version="latest"
prefix="${HOME}/.local"
dist_dir="${GWT_DIST_DIR:-}"
uninstall=0
dry_run=0

while [ $# -gt 0 ]; do
	case "$1" in
		-h|--help) usage; exit 0 ;;
		--uninstall) uninstall=1; shift ;;
		--dry-run) dry_run=1; shift ;;
		--version=*) version="${1#*=}"; shift ;;
		--prefix=*) prefix="${1#*=}"; shift ;;
		--dist-dir=*) dist_dir="${1#*=}"; shift ;;
		--version|--prefix|--dist-dir)
			opt="$1"
			shift
			[ $# -gt 0 ] || die "$opt 需要一个取值"
			case "$opt" in
				--version) version="$1" ;;
				--prefix) prefix="$1" ;;
				--dist-dir) dist_dir="$1" ;;
			esac
			shift
			;;
		--) shift; break ;;
		-*) die "未知参数: $1（用 --help 看用法）" ;;
		*) die "不接受位置参数: $1（用 --help 看用法）" ;;
	esac
done
[ $# -eq 0 ] || die "多余的位置参数: $*"

[ -n "$prefix" ] || die "--prefix 不能为空"
target="$prefix/bin/$PROG"

# ---------------------------------------------------------------- 卸载
if [ "$uninstall" -eq 1 ]; then
	if [ -e "$target" ]; then
		if [ "$dry_run" -eq 1 ]; then
			say "[dry-run] 将删除 $target"
		else
			rm -f "$target"
			say "已卸载: $target"
		fi
	else
		say "未找到 $target，无需卸载"
	fi
	exit 0
fi

# ---------------------------------------------------------------- 平台判定
os=$(uname -s)
[ "$os" = "Linux" ] || die "只支持 Linux（当前 '$os'）。gwt 用 Linux ioctl 探测终端与宽度，macOS/BSD/Windows 尚未适配。"

machine=$(uname -m)
case "$machine" in
	x86_64|amd64) goarch=amd64 ;;
	aarch64|arm64) goarch=arm64 ;;
	i386|i486|i586|i686) goarch=386 ;;
	riscv64) goarch=riscv64 ;;
	*) die "不支持的架构: $machine（已支持: x86_64 / aarch64 / i686 / riscv64）" ;;
esac
asset="gwt-linux-$goarch"

# ---------------------------------------------------------------- 取版本号
# 只有联网模式才需要解析 latest；离线目录模式绝不碰网络
if [ -z "$dist_dir" ] && [ "$version" = "latest" ]; then
	resolved=""
	if have curl; then
		resolved=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
			"https://github.com/${REPO}/releases/latest" 2>/dev/null) || resolved=""
	elif have wget; then
		resolved=$(wget --server-response --spider \
			"https://github.com/${REPO}/releases/latest" 2>&1 |
			awk '/^ *Location:/{v=$2} END{print v}') || resolved=""
	else
		die "需要 curl 或 wget"
	fi
	resolved=${resolved##*/}
	case "$resolved" in
		v[0-9]*) version="$resolved" ;;
		*) die "拿不到最新正式版本（解析结果: '${resolved:-空}'）。仓库可能还没有 Release，请显式指定，例如 --version 0.3.0" ;;
	esac
fi

case "$version" in
	v*) tag="$version"; disp="${version#v}" ;;
	*) tag="v$version"; disp="$version" ;;
esac

# ---------------------------------------------------------------- 取文件
tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/gwt-install.XXXXXX") || die "无法创建临时目录"
trap 'rm -rf "$tmpdir"' EXIT HUP INT TERM

fetch() { # $1=url $2=dest
	if have curl; then
		curl -fsSL --proto '=https' --proto-redir '=https' -o "$2" "$1"
	elif have wget; then
		wget -q -O "$2" "$1"
	else
		die "需要 curl 或 wget 其中之一"
	fi
}

if [ -n "$dist_dir" ]; then
	src="${dist_dir%/}"
	[ -d "$src" ] || die "--dist-dir 不是目录: $src"
	if [ "$dry_run" -eq 1 ]; then
		say "[dry-run] 将从本地目录 $src 读取 $asset 与 SHA256SUMS"
	else
		[ -f "$src/$asset" ] || die "读不到 $src/$asset"
		[ -f "$src/SHA256SUMS" ] || die "读不到 $src/SHA256SUMS —— 拒绝在没有校验和的情况下安装"
		cp "$src/$asset" "$tmpdir/$asset"
		cp "$src/SHA256SUMS" "$tmpdir/SHA256SUMS"
	fi
else
	base="$BASE_URL/$tag"
	if [ "$dry_run" -eq 1 ]; then
		say "[dry-run] 将从 $base 下载 $asset 与 SHA256SUMS"
	else
		say "正在下载 $PROG $disp ($asset) ..."
		fetch "$base/$asset" "$tmpdir/$asset" || die "下载 $asset 失败（$tag 这个版本存在吗？）"
		fetch "$base/SHA256SUMS" "$tmpdir/SHA256SUMS" || die "下载 SHA256SUMS 失败 —— 拒绝在没有校验和的情况下安装"
	fi
fi

# ---------------------------------------------------------------- 校验 sha256
if [ "$dry_run" -eq 0 ]; then
	expected=$(awk -v a="$asset" '$2==a {print $1}' "$tmpdir/SHA256SUMS" | head -n 1)
	[ -n "$expected" ] || die "SHA256SUMS 里没有 $asset 的条目 —— 拒绝安装"

	if have sha256sum; then
		got=$(sha256sum "$tmpdir/$asset" | awk '{print $1}')
	elif have shasum; then
		got=$(shasum -a 256 "$tmpdir/$asset" | awk '{print $1}')
	elif have busybox; then
		got=$(busybox sha256sum "$tmpdir/$asset" | awk '{print $1}')
	else
		die "找不到 sha256sum / shasum / busybox，无法校验 —— 拒绝安装"
	fi

	[ "$got" = "$expected" ] || die "校验和不匹配！期望 $expected，实际 $got —— 已放弃安装"
	say "sha256 校验通过"
fi

# ---------------------------------------------------------------- 安装
bindir="$prefix/bin"
if [ "$dry_run" -eq 1 ]; then
	say "[dry-run] 将把 $asset 安装为 $bindir/$PROG"
	say "[dry-run] 结束（未改动文件系统）"
	exit 0
fi

mkdir -p "$bindir" 2>/dev/null ||
	die "无法创建 $bindir —— 换 --prefix \"\$HOME/.local\"，或加 sudo 后重试"
[ -w "$bindir" ] ||
	die "$bindir 不可写 —— 换 --prefix \"\$HOME/.local\"，或加 sudo 后重试"

if have install; then
	install -m755 "$tmpdir/$asset" "$bindir/$PROG"
else
	cp "$tmpdir/$asset" "$bindir/$PROG"
	chmod 755 "$bindir/$PROG"
fi

say "已安装: $bindir/$PROG ($("$bindir/$PROG" --version 2>/dev/null || printf '版本未知'))"

case ":${PATH}:" in
	*":$bindir:"*) ;;
	*) warn "提示: $bindir 不在 PATH 中；把它加入 shell 配置（如 ~/.bashrc）后就能直接用 $PROG" ;;
esac

have git || warn "注意: 未找到 git —— $PROG 运行时需要 git（Arch: pacman -S git / Debian: apt install git）"
