#!/bin/sh
# Install Tailarr from a GitHub release (checksum verified).
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/jackspiering/tailarr/main/scripts/install.sh | sh
#   curl -fsSL https://github.com/jackspiering/tailarr/releases/download/v0.2.0/install.sh | sh
#
# Environment:
#   TAILARR_VERSION  Release tag (e.g. v0.2.0). Default: latest via GitHub API, else v0.2.0
#   INSTALL_DIR      Install directory. Default: /usr/local/bin if writable, else ~/.local/bin
#   GITHUB_REPO      owner/repo (default: jackspiering/tailarr)
#
# Requires: curl or wget; sha256sum or shasum.

set -eu

REPO="${GITHUB_REPO:-jackspiering/tailarr}"
BINARY_NAME="tailarr"
DEFAULT_VERSION="v0.2.0"
API_URL="https://api.github.com/repos/${REPO}/releases/latest"
RELEASES_URL="https://github.com/${REPO}/releases/download"

info() { printf '%s\n' "$*"; }
err() { printf 'Error: %s\n' "$*" >&2; exit 1; }

need_cmd() {
	command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"
}

download() {
	# download <url> <dest>
	url=$1
	dest=$2
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$url" -o "$dest"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$dest" "$url"
	else
		err "need curl or wget to download releases"
	fi
}

download_to_stdout() {
	url=$1
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$url"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO- "$url"
	else
		err "need curl or wget to download releases"
	fi
}

detect_os() {
	os=$(uname -s | tr '[:upper:]' '[:lower:]')
	case "$os" in
	linux | darwin) printf '%s\n' "$os" ;;
	*) err "unsupported OS: $(uname -s) (supported: linux, darwin)" ;;
	esac
}

detect_arch() {
	arch=$(uname -m)
	case "$arch" in
	x86_64 | amd64) printf 'amd64\n' ;;
	aarch64 | arm64) printf 'arm64\n' ;;
	*) err "unsupported architecture: $arch (supported: amd64, arm64)" ;;
	esac
}

resolve_version() {
	if [ -n "${TAILARR_VERSION:-}" ]; then
		v=$TAILARR_VERSION
		case "$v" in
		v*) printf '%s\n' "$v" ;;
		*) printf 'v%s\n' "$v" ;;
		esac
		return
	fi
	# Latest release tag from GitHub API (best effort).
	if tag=$(download_to_stdout "$API_URL" 2>/dev/null | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1); then
		if [ -n "$tag" ]; then
			printf '%s\n' "$tag"
			return
		fi
	fi
	printf '%s\n' "$DEFAULT_VERSION"
}

resolve_install_dir() {
	if [ -n "${INSTALL_DIR:-}" ]; then
		printf '%s\n' "$INSTALL_DIR"
		return
	fi
	if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
		printf '/usr/local/bin\n'
		return
	fi
	# Not writable (common without sudo): prefer user local bin.
	printf '%s\n' "${HOME}/.local/bin"
}

sha256_file() {
	file=$1
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$file" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$file" | awk '{print $1}'
	else
		err "need sha256sum or shasum to verify downloads"
	fi
}

verify_checksum() {
	# verify_checksum <sums_file> <asset_name> <binary_path>
	sums=$1
	name=$2
	bin=$3
	expected=$(awk -v n="$name" '$2 == n { print $1; exit }' "$sums")
	if [ -z "$expected" ]; then
		err "checksum for $name not found in SHA256SUMS"
	fi
	actual=$(sha256_file "$bin")
	if [ "$expected" != "$actual" ]; then
		err "checksum mismatch for $name (expected $expected, got $actual)"
	fi
}

main() {
	os=$(detect_os)
	arch=$(detect_arch)
	version=$(resolve_version)
	install_dir=$(resolve_install_dir)
	asset="${BINARY_NAME}-${os}-${arch}"
	base="${RELEASES_URL}/${version}"

	info "Installing Tailarr ${version} (${os}/${arch})"
	info "Destination: ${install_dir}/${BINARY_NAME}"

	tmpdir=$(mktemp -d 2>/dev/null || mktemp -d -t tailarr-install)
	cleanup() { rm -rf "$tmpdir"; }
	trap cleanup EXIT INT TERM

	info "Downloading ${asset} ..."
	download "${base}/${asset}" "${tmpdir}/${asset}"
	info "Downloading SHA256SUMS ..."
	download "${base}/SHA256SUMS" "${tmpdir}/SHA256SUMS"
	verify_checksum "${tmpdir}/SHA256SUMS" "$asset" "${tmpdir}/${asset}"
	info "Checksum OK"

	mkdir -p "$install_dir"
	if [ ! -w "$install_dir" ]; then
		err "install directory is not writable: $install_dir (set INSTALL_DIR or run with permissions to write there)"
	fi

	# Atomic-ish install: write then rename into place.
	tmp_dest="${install_dir}/.${BINARY_NAME}.install.$$"
	cp "${tmpdir}/${asset}" "$tmp_dest"
	chmod 755 "$tmp_dest"
	mv -f "$tmp_dest" "${install_dir}/${BINARY_NAME}"

	installed="${install_dir}/${BINARY_NAME}"
	info "Installed: ${installed}"

	# Version smoke check when binary is executable here.
	if "${installed}" version >/dev/null 2>&1; then
		info "Version: $("${installed}" version)"
	fi

	case ":${PATH}:" in
	*":${install_dir}:"*) ;;
	*)
		info ""
		info "Note: ${install_dir} is not on your PATH."
		info "Add it, for example:"
		info "  export PATH=\"${install_dir}:\$PATH\""
		;;
	esac

	info ""
	info "Next steps:"
	info "  tailarr doctor"
	info "  tailarr              # TUI when stdout is a TTY"
	info "  tailarr --help"
}

main
