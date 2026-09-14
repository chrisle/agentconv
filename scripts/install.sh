#!/bin/sh
set -eu

repo="chrisle/agentconv"

case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *) echo "agentconv: unsupported operating system: $(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  arm64|aarch64) arch="arm64" ;;
  x86_64|amd64) arch="amd64" ;;
  *) echo "agentconv: unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

asset="agentconv-${os}-${arch}"
if [ "$os" = "darwin" ]; then
  asset="${asset}.zip"
fi
url="https://github.com/${repo}/releases/latest/download/${asset}"
destination="${HOME}/.local/bin"

mkdir -p "$destination"
if [ "$os" = "darwin" ]; then
  archive=$(mktemp)
  trap 'rm -f "$archive"' EXIT
  curl -fsSL "$url" -o "$archive"
  unzip -p "$archive" > "$destination/agentconv"
else
  curl -fsSL "$url" -o "$destination/agentconv"
fi
chmod 755 "$destination/agentconv"
echo "Installed agentconv to $destination/agentconv"
echo "Ensure $destination is on your PATH."
