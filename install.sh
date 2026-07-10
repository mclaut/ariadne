#!/bin/sh
# Ariadne one-command installer (Linux + macOS). Nothing to install by hand:
#
#   curl -fsSL https://raw.githubusercontent.com/mclaut/ariadne/main/install.sh | sh
#
# Bootstraps Go (official tarball) and the source (GitHub tarball — no git needed),
# then runs the Go installer, which auto-installs Ollama, Qdrant, the models, the
# services, the tray and its deps. Pass installer flags through after `-s --`:
#
#   curl -fsSL .../install.sh | sh -s -- -summary-model qwen2.5:3b
#   curl -fsSL .../install.sh | sh -s -- -dry-run
#
# Set ARIADNE_REF to install an exact release tag (used by the tray updater).
# ARIADNE_BRANCH remains as a backwards-compatible alias.
#
# sudo (for apt packages + Ollama on Linux) prompts on the terminal, so the
# curl | sh pipe is fine. Windows: use the PowerShell path (see README).
set -eu

REPO="mclaut/ariadne"
REF="${ARIADNE_REF:-${ARIADNE_BRANCH:-main}}"
SRC="${ARIADNE_SRC:-$HOME/.ariadne/src}"
GOLOCAL="$HOME/.ariadne/go"
GO_VERSION="${ARIADNE_GO_VERSION:-go1.26.2}"

say() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die "need sha256sum or shasum to verify downloads"
  fi
}

download_verified() {
  url="$1"
  want="$2"
  dest="$3"
  tmp="${dest}.tmp.$$"
  curl -fsSL "$url" -o "$tmp"
  got="$(sha256_file "$tmp")"
  if [ "$got" != "$want" ]; then
    rm -f "$tmp"
    die "checksum mismatch for $url: got $got, want $want"
  fi
  mv "$tmp" "$dest"
}

os="$(uname -s)"
arch="$(uname -m)"
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) die "unsupported CPU arch: $arch" ;;
esac
case "$os" in
  Linux) goos=linux ;;
  Darwin) goos=darwin ;;
  *) die "unsupported OS: $os — on Windows use the PowerShell path (see README)" ;;
esac

# 1. Go — reuse a recent system Go (>=1.21 has toolchain auto-fetch), else drop
#    the official tarball into ~/.ariadne/go. No apt/snap, no sudo for this.
go_ok=0
if command -v go >/dev/null 2>&1; then
  minor="$(go version | awk '{print $3}' | sed 's/^go//' | cut -d. -f2)"
  case "$minor" in
    '' | *[!0-9]*) : ;;
    *) [ "$minor" -ge 21 ] && go_ok=1 ;;
  esac
fi
if [ "$go_ok" -eq 1 ]; then
  say "Go present: $(go version)"
else
  gover="$GO_VERSION"
  go_url="https://go.dev/dl/${gover}.${goos}-${arch}.tar.gz"
  go_sha="${ARIADNE_GO_SHA256:-$(curl -fsSL "${go_url}.sha256" | awk '{print $1}')}"
  [ -n "$go_sha" ] || die "could not determine checksum for $go_url"
  say "Installing $gover -> $GOLOCAL"
  mkdir -p "$HOME/.ariadne"
  rm -rf "$GOLOCAL"
  tmp_tar="$HOME/.ariadne/${gover}.${goos}-${arch}.tar.gz"
  download_verified "$go_url" "$go_sha" "$tmp_tar"
  tar -C "$HOME/.ariadne" -xzf "$tmp_tar"
  rm -f "$tmp_tar"
  PATH="$GOLOCAL/bin:$PATH"
  export PATH
  say "Go installed: $(go version)"
fi

# 2. Source — GitHub branch tarball (no git dependency).
say "Fetching Ariadne source ($REF) -> $SRC"
mkdir -p "$SRC"
curl -fsSL "https://github.com/${REPO}/archive/${REF}.tar.gz" |
  tar -xz -C "$SRC" --strip-components=1

# 3. Hand off to the Go installer (Ollama, Qdrant, models, services, tray + deps).
say "Running the installer..."
cd "$SRC"
exec go run ./cmd/install -yes "$@"
