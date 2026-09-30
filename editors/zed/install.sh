#!/bin/sh
# Builds the Zed extension and installs it the way Zed installs extensions
# from its registry, without needing rustc on Zed's own PATH. Remove it by
# deleting the installed directory printed below.
set -eu
cd "$(dirname "$0")"

RUSTUP_TOOLCHAIN="${RUSTUP_TOOLCHAIN:-$(sed -n 's/^channel = "\(.*\)"/\1/p' rust-toolchain.toml)}" \
	cargo build --release --target wasm32-wasip2 --quiet

case "$(uname)" in
Darwin) dir="$HOME/Library/Application Support/Zed/extensions/installed/melange" ;;
*) dir="${XDG_DATA_HOME:-$HOME/.local/share}/zed/extensions/installed/melange" ;;
esac
api=$(sed -n 's/^zed_extension_api = "\(.*\)"/\1/p' Cargo.toml)

mkdir -p "$dir"
cp target/wasm32-wasip2/release/zed_melange.wasm "$dir/extension.wasm"
# Zed records the extension API version in [lib] when it packages extensions.
{ cat extension.toml; printf '\n[lib]\nkind = "Rust"\nversion = "%s"\n' "$api"; } >"$dir/extension.toml"
echo "installed to $dir"
