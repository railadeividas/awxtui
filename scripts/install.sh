#!/bin/sh
# Install awxtui from a GitHub Release.
#
# Usage:
#   curl -fsSL https://github.com/railadeividas/awxtui/releases/latest/download/install.sh | sh
#   AWXTUI_VERSION=v0.1.0 sh install.sh
#   AWXTUI_INSTALL_DIR=/usr/local/bin sh install.sh
set -eu

repo="railadeividas/awxtui"
install_dir="${AWXTUI_INSTALL_DIR:-$HOME/.local/bin}"

case "$(uname -s)" in
Linux) os="linux" ;;
Darwin) os="darwin" ;;
*)
	echo "awxtui installer: unsupported operating system: $(uname -s)" >&2
	exit 1
	;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch="amd64" ;;
aarch64 | arm64) arch="arm64" ;;
*)
	echo "awxtui installer: unsupported CPU architecture: $(uname -m)" >&2
	exit 1
	;;
esac

tag="${AWXTUI_VERSION:-}"
if [ -z "$tag" ]; then
	tag="$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" |
		sed -n 's/^[[:space:]]*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' |
		head -n 1)"
fi
if [ -z "$tag" ]; then
	echo "awxtui installer: could not determine the latest release" >&2
	exit 1
fi

version="${tag#v}"
archive="awxtui_${version}_${os}_${arch}.tar.gz"
base_url="https://github.com/$repo/releases/download/$tag"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' 0 HUP INT TERM

curl -fsSL "$base_url/$archive" -o "$tmpdir/$archive"
curl -fsSL "$base_url/checksums.txt" -o "$tmpdir/checksums.txt"

expected="$(awk -v file="$archive" '$2 == file { print $1 }' "$tmpdir/checksums.txt")"
if [ -z "$expected" ]; then
	echo "awxtui installer: checksum for $archive was not published" >&2
	exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
	actual="$(sha256sum "$tmpdir/$archive" | awk '{print $1}')"
else
	actual="$(shasum -a 256 "$tmpdir/$archive" | awk '{print $1}')"
fi
if [ "$actual" != "$expected" ]; then
	echo "awxtui installer: checksum verification failed" >&2
	exit 1
fi

tar -xzf "$tmpdir/$archive" -C "$tmpdir"
mkdir -p "$install_dir"
install -m 0755 "$tmpdir/awxtui" "$install_dir/awxtui"

echo "Installed awxtui $tag to $install_dir/awxtui"
case ":$PATH:" in
*":$install_dir:"*) ;;
*) echo "Add $install_dir to your PATH to run awxtui." ;;
esac
