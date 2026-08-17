#!/bin/sh
#
# Astra Harness one-line installer (Linux / macOS).
#
#   curl -fsSL https://astracode.topodrive.top/install/install.sh | sh
#
# Override the download base or target directory with:
#   ASTRA_BASE_URL=https://... ASTRA_INSTALL_DIR=/opt/astra ./install.sh
set -eu

BASE_URL="${ASTRA_BASE_URL:-https://astracode.topodrive.top/install}"
INSTALL_DIR="${ASTRA_INSTALL_DIR:-$HOME/.local/bin}"

os="$(uname -s)"
arch="$(uname -m)"

case "$arch" in
  x86_64|amd64) target_arch="amd64" ;;
  aarch64|arm64) target_arch="arm64" ;;
  *)
    echo "error: unsupported architecture: $arch" >&2
    exit 1
    ;;
esac

case "$os" in
  Linux) asset="astra-linux-${target_arch}.tar.gz" ;;
  Darwin) asset="astra-darwin-${target_arch}.tar.gz" ;;
  *)
    echo "error: unsupported OS: $os (use the Windows installer instead)" >&2
    exit 1
    ;;
esac

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT HUP INT TERM

echo "Downloading $asset ..."
curl -fsSL "$BASE_URL/$asset" -o "$tmpdir/$asset"
curl -fsSL "$BASE_URL/sha256sums.txt" -o "$tmpdir/sha256sums.txt"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$tmpdir" && grep "^[0-9a-f]\{64\}  $asset$" sha256sums.txt | sha256sum -c - >/dev/null)
elif command -v shasum >/dev/null 2>&1; then
  (cd "$tmpdir" && grep "^[0-9a-f]\{64\}  $asset$" sha256sums.txt | shasum -a 256 -c - >/dev/null)
else
  echo "warning: no sha256sum/shasum found; skipping checksum" >&2
fi

tar -xzf "$tmpdir/$asset" -C "$tmpdir"
mkdir -p "$INSTALL_DIR"
install -m 0755 "$tmpdir/astra" "$INSTALL_DIR/astra"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    for rc in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile"; do
      [ -f "$rc" ] || continue
      grep -Fq "$INSTALL_DIR" "$rc" 2>/dev/null && continue
      printf '\n# added by astra installer\nexport PATH="%s:$PATH"\n' "$INSTALL_DIR" >> "$rc"
    done
    echo "Added $INSTALL_DIR to your shell config (new terminals will pick it up)."
    ;;
esac

# Make `astra` available in the current shell too.
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    export PATH="$INSTALL_DIR:$PATH"
    echo "Updated PATH for this shell."
    ;;
esac

"$INSTALL_DIR/astra" version
echo "Astra installed: $INSTALL_DIR/astra"
