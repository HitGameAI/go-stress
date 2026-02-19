#!/bin/sh
# go-stress 一键安装脚本
#
# 用法:
#   curl -fsSL https://raw.githubusercontent.com/kamalyes/go-stress/master/install.sh | sh
#   或 wget -qO- https://raw.githubusercontent.com/kamalyes/go-stress/master/install.sh | sh
#
# 行为: 检测系统与架构，从 GitHub Releases 下载对应预编译二进制安装到
#       /usr/local/bin（不可写时回落 ~/.local/bin），并给出 PATH 提示；
#       Releases 尚无产物或下载失败时，若本机有 Go 则回落 go install。
set -e

REPO="kamalyes/go-stress"
BASE_URL="https://github.com/${REPO}/releases"

# ---------- 检测系统与架构 ----------
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux)   OS="linux" ;;
  darwin)  OS="darwin" ;;
  freebsd) OS="freebsd" ;;
  openbsd) OS="openbsd" ;;
  netbsd)  OS="netbsd" ;;
  *)
    echo "❌ 暂不支持当前系统: $(uname -s)"
    echo "   Windows 请到 $BASE_URL 直接下载 go-stress-windows-amd64.exe"
    exit 1
    ;;
esac

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  i386|i686)      ARCH="386" ;;
  ppc64le)        ARCH="ppc64le" ;;
  s390x)          ARCH="s390x" ;;
  mips64)         ARCH="mips64" ;;
  mips64le)       ARCH="mips64le" ;;
  *)
    echo "❌ 暂不支持当前架构: $(uname -m)"
    exit 1
    ;;
esac

ASSET="go-stress-${OS}-${ARCH}"

# ---------- 选择安装目录 ----------
if [ -w /usr/local/bin ]; then
  INSTALL_DIR="/usr/local/bin"
elif command -v sudo >/dev/null 2>&1; then
  INSTALL_DIR="/usr/local/bin"
else
  INSTALL_DIR="${HOME}/.local/bin"
  mkdir -p "$INSTALL_DIR"
fi

# ---------- 下载工具 ----------
download() { # download <url> <输出文件>
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1"
  else
    echo "❌ 需要 curl 或 wget，请先安装其一"
    exit 1
  fi
}

BIN="${INSTALL_DIR}/go-stress"
echo "⬇️  下载 ${ASSET} ..."

# 优先: Releases 预编译二进制（免编译，秒装）
if download "${BASE_URL}/latest/download/${ASSET}" "${BIN}.tmp" 2>/dev/null && [ -s "${BIN}.tmp" ]; then
  if [ "$INSTALL_DIR" = "/usr/local/bin" ] && [ ! -w /usr/local/bin ]; then
    sudo mv "${BIN}.tmp" "$BIN"
    sudo chmod +x "$BIN"
  else
    mv "${BIN}.tmp" "$BIN"
    chmod +x "$BIN"
  fi
else
  # 回落: 本机 Go 编译安装
  rm -f "${BIN}.tmp"
  echo "⚠️  未从 Releases 下载到 ${ASSET}（可能尚未发布），尝试回落 go install"
  if command -v go >/dev/null 2>&1; then
    GOBIN="$(go env GOPATH)/bin" go install "github.com/${REPO}@latest"
    BIN="$(go env GOPATH)/bin/go-stress"
  else
    echo "❌ 下载失败且本机未安装 Go，无法完成安装"
    echo "   可到 $BASE_URL 手动下载，或先安装 Go: https://go.dev/dl/"
    exit 1
  fi
fi

# ---------- 验证 ----------
VERSION="$("$BIN" version 2>/dev/null || echo unknown)"
echo ""
echo "✅ 安装完成: $BIN ($VERSION)"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "⚠️  $INSTALL_DIR 不在 PATH 中，请将其加入（如追加到 ~/.profile）" ;;
esac
echo ""
echo "🚀 试一下:"
echo "   go-stress -url https://api.example.com -c 10 -n 100"
