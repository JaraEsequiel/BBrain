#!/bin/sh
# BBrain installer — downloads a prebuilt binary from GitHub Releases.
# No Go toolchain required. Works on macOS and Linux (amd64 / arm64).
#
#   curl -fsSL https://raw.githubusercontent.com/JaraEsequiel/BBrain/master/install.sh | sh
#
# Env overrides:
#   BBRAIN_VERSION   tag to install (default: latest)
#   BBRAIN_BIN_DIR   install dir (default: /usr/local/bin, else ~/.local/bin)
set -eu

REPO="JaraEsequiel/BBrain"

err() { echo "install: $*" >&2; exit 1; }

# --- detect platform ---------------------------------------------------------
os=$(uname -s)
case "$os" in
	Linux)  OS=linux ;;
	Darwin) OS=darwin ;;
	*) err "unsupported OS: $os (only Linux and macOS have prebuilt binaries; build from source with 'go install $REPO/cmd/bbrain@latest')" ;;
esac

arch=$(uname -m)
case "$arch" in
	x86_64|amd64)  ARCH=amd64 ;;
	arm64|aarch64) ARCH=arm64 ;;
	*) err "unsupported architecture: $arch" ;;
esac

asset="bbrain_${OS}_${ARCH}.tar.gz"

# --- resolve download URL ----------------------------------------------------
if [ "${BBRAIN_VERSION:-latest}" = "latest" ]; then
	url="https://github.com/$REPO/releases/latest/download/$asset"
else
	url="https://github.com/$REPO/releases/download/${BBRAIN_VERSION}/$asset"
fi

# --- fetch + extract ---------------------------------------------------------
if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO "$2" "$1"; }
else
	err "need curl or wget"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "install: downloading $asset ..."
fetch "$url" "$tmp/bbrain.tar.gz" || err "download failed: $url"
tar -xzf "$tmp/bbrain.tar.gz" -C "$tmp" || err "extract failed"
[ -f "$tmp/bbrain" ] || err "archive did not contain a 'bbrain' binary"
chmod +x "$tmp/bbrain"

# --- choose install dir + place binary ---------------------------------------
dir="${BBRAIN_BIN_DIR:-/usr/local/bin}"
if [ -w "$dir" ] 2>/dev/null || { [ ! -e "$dir" ] && mkdir -p "$dir" 2>/dev/null; }; then
	mv "$tmp/bbrain" "$dir/bbrain"
elif [ "${BBRAIN_BIN_DIR:-}" = "" ] && command -v sudo >/dev/null 2>&1 && \
     echo "install: $dir needs elevated permissions, trying sudo ..." && \
     sudo mv "$tmp/bbrain" "$dir/bbrain"; then
	: # installed via sudo
else
	# no write access and sudo unavailable/declined/denied — fall back to a
	# user-writable dir instead of aborting.
	dir="$HOME/.local/bin"
	mkdir -p "$dir"
	mv "$tmp/bbrain" "$dir/bbrain"
	echo "install: used $dir (no permission for ${BBRAIN_BIN_DIR:-/usr/local/bin})"
fi

echo "install: installed bbrain to $dir/bbrain"
case ":$PATH:" in
	*":$dir:"*) ;;
	*) echo "install: NOTE — $dir is not on your PATH. Add it:  export PATH=\"$dir:\$PATH\"" ;;
esac

"$dir/bbrain" version 2>/dev/null || true
echo "install: next, wire it into Claude Code with:  bbrain install"
