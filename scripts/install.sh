#!/bin/sh
# Install Tailarr from a GitHub release (checksum verified).
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/jackspiering/tailarr/main/scripts/install.sh | sh
#   curl -fsSL https://github.com/jackspiering/tailarr/releases/download/v0.2.0/install.sh | sh
#
# Environment:
#   TAILARR_VERSION  Release tag (e.g. v0.2.0). Default: latest via GitHub API, else v0.4.0
#   INSTALL_DIR      Install directory. Default: directory of the first `tailarr` on
#                    PATH if writable (replaces legacy installs), else /usr/local/bin
#                    if writable, else ~/.local/bin
#   GITHUB_REPO      owner/repo (default: jackspiering/tailarr)
#
# Requires: curl or wget; sha256sum or shasum.

set -eu

REPO="${GITHUB_REPO:-jackspiering/tailarr}"
BINARY_NAME="tailarr"
DEFAULT_VERSION="v0.4.0"
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
	printf 'Warning: could not resolve the latest release from %s; falling back to %s\n' "$API_URL" "$DEFAULT_VERSION" >&2
	printf '%s\n' "$DEFAULT_VERSION"
}

resolve_install_dir() {
	if [ -n "${INSTALL_DIR:-}" ]; then
		printf '%s\n' "$INSTALL_DIR"
		return
	fi

	# Prefer replacing whatever `tailarr` would run today. That fixes the common
	# case where a legacy tool lives in ~/.local/bin ahead of /usr/local/bin.
	hash -r 2>/dev/null || true
	existing=$(command -v "${BINARY_NAME}" 2>/dev/null || true)
	if [ -n "$existing" ]; then
		existing_dir=$(dirname "$existing")
		if [ -w "$existing_dir" ]; then
			# We never run the existing file to classify it; if the PATH entry
			# still differs after install, warn_path_shadow reports it.
			info "Replacing existing '${BINARY_NAME}' at ${existing}" >&2
			printf '%s\n' "$existing_dir"
			return
		fi
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
	tmp_dest=
	cleanup() {
		rm -rf "$tmpdir"
		rm -f "$tmp_dest"
	}
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

	# Atomic-ish install: write then rename into place. mktemp creates the
	# temp file with O_EXCL (mode 0600), so a planted symlink at the fixed
	# name cannot be followed; widen to 755 before the rename. The trap
	# removes any leftover on failure.
	tmp_dest=$(mktemp "${install_dir}/.${BINARY_NAME}.install.XXXXXX")
	cp "${tmpdir}/${asset}" "$tmp_dest"
	chmod 755 "$tmp_dest"
	mv -f "$tmp_dest" "${install_dir}/${BINARY_NAME}"

	installed="${install_dir}/${BINARY_NAME}"
	info "Installed: ${installed}"

	# Identity smoke check on the file we just wrote (full path). The TUI-only
	# binary refuses non-TTY runs; expect exactly that refusal.
	installed_out=$("${installed}" </dev/null 2>&1 || true)
	case "${installed_out}" in
	*"Tailarr is interactive"*)
		info "Verified: ${installed} (TUI-only Tailarr)"
		;;
	*)
		err "unexpected output from ${installed}: ${installed_out}"
		;;
	esac

	case ":${PATH}:" in
	*":${install_dir}:"*) ;;
	*)
		info ""
		info "Note: ${install_dir} is not on your PATH."
		info "Add it, for example:"
		info "  export PATH=\"${install_dir}:\$PATH\""
		;;
	esac

	# Detect a different tailarr earlier on PATH (common with legacy installs).
	# Shells may also cache a previous location (bash: hash -r).
	warn_path_shadow "${installed}"

	info ""
	info "Next steps:"
	info "  ${installed}              # interactive TUI (run inside a terminal)"
	if command -v "${BINARY_NAME}" >/dev/null 2>&1; then
		resolved=$(command -v "${BINARY_NAME}" 2>/dev/null || true)
		if [ -n "${resolved}" ] && same_file "${resolved}" "${installed}"; then
			info ""
			info "Or, if ${install_dir} is first on your PATH:"
			info "  tailarr"
		fi
	fi
}

# same_file returns 0 if both paths exist and refer to the same inode (best effort).
same_file() {
	a=$1
	b=$2
	[ -e "$a" ] && [ -e "$b" ] || return 1
	# Prefer portable stat when available; fall back to cmp of resolved paths.
	if command -v stat >/dev/null 2>&1; then
		# GNU and BSD stat differ; try GNU first, then BSD.
		ida=$(stat -c '%d:%i' "$a" 2>/dev/null || stat -f '%d:%i' "$a" 2>/dev/null || true)
		idb=$(stat -c '%d:%i' "$b" 2>/dev/null || stat -f '%d:%i' "$b" 2>/dev/null || true)
		if [ -n "$ida" ] && [ -n "$idb" ] && [ "$ida" = "$idb" ]; then
			return 0
		fi
	fi
	# Path string compare after cd -P style resolve is hard in pure sh; use cmp.
	cmp -s "$a" "$b" 2>/dev/null
}

warn_path_shadow() {
	installed=$1

	# Clear bash command hash so we do not report a stale cached path.
	# hash is a bash builtin; ignore failures under plain sh/dash.
	hash -r 2>/dev/null || true

	resolved=$(command -v "${BINARY_NAME}" 2>/dev/null || true)
	if [ -z "${resolved}" ]; then
		info ""
		info "Warning: '${BINARY_NAME}' was not found on PATH after install."
		info "Use the full path: ${installed}"
		return
	fi

	if same_file "${resolved}" "${installed}"; then
		# PATH entry is the binary we just installed; nothing to warn about.
		return
	fi

	info ""
	info "WARNING: another program named '${BINARY_NAME}' is first on your PATH."
	info "  PATH resolves to: ${resolved}"
	info "  Go Tailarr is at: ${installed}"
	info ""
	info "The Go install succeeded, but running bare 'tailarr' may invoke a legacy tool."
	info "Fix (pick one):"
	info "  1. Run the Go binary by full path:"
	info "       ${installed}"
	install_dir=$(dirname "${installed}")
	info "  2. Put ${install_dir} earlier on PATH than the directory of ${resolved}"
	info "  3. Rename or remove the other binary if you no longer need it:"
	info "       mv ${resolved} ${resolved}.legacy"
	info "  4. If your shell cached the old path (bash): hash -r"
	info ""
	info "List all matches with:"
	info "  type -a ${BINARY_NAME}    # bash/zsh"
	info "  which -a ${BINARY_NAME}   # some systems"
}

main
