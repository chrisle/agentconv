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
url="https://github.com/${repo}/releases/latest/download/${asset}"
destination="${HOME}/.local/bin"

mkdir -p "$destination"
curl -fsSL "$url" -o "$destination/agentconv"
chmod 755 "$destination/agentconv"
echo "Installed agentconv to $destination/agentconv"
echo "Ensure $destination is on your PATH."
