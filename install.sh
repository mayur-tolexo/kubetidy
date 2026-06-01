#!/usr/bin/env sh
# kubetidy installer — download the latest (or a pinned) release, verify its checksum, and
# install both faces (`kubetidy` and the `kubectl-tidy` kubectl plugin) onto your PATH.
#
#   curl -fsSL https://raw.githubusercontent.com/mayur-tolexo/kubetidy/main/install.sh | sh
#
# Environment overrides:
#   KUBETIDY_VERSION   tag to install (default: latest release, e.g. v1.2.3)
#   KUBETIDY_BIN_DIR   install directory (default: /usr/local/bin, or ~/.local/bin if not writable)
#   KUBETIDY_NO_PLUGIN set to any value to skip installing the kubectl-tidy plugin copy
set -eu

REPO="mayur-tolexo/kubetidy"
BINARY="kubetidy"

log()  { printf '%s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# --- detect os/arch (matching goreleaser archive names) ---------------------------------------
detect_os() {
	os="$(uname -s | tr '[:upper:]' '[:lower:]')"
	case "$os" in
		linux) echo linux ;;
		darwin) echo darwin ;;
		*) die "unsupported OS: $os (use krew or build from source)" ;;
	esac
}

detect_arch() {
	arch="$(uname -m)"
	case "$arch" in
		x86_64|amd64) echo amd64 ;;
		arm64|aarch64) echo arm64 ;;
		*) die "unsupported architecture: $arch (use krew or build from source)" ;;
	esac
}

# --- download helpers -------------------------------------------------------------------------
fetch() { # fetch <url> <dest>
	if have curl; then
		curl -fsSL "$1" -o "$2"
	elif have wget; then
		wget -qO "$2" "$1"
	else
		die "need curl or wget to download releases"
	fi
}

fetch_stdout() { # fetch_stdout <url>
	if have curl; then
		curl -fsSL "$1"
	elif have wget; then
		wget -qO - "$1"
	else
		die "need curl or wget to download releases"
	fi
}

latest_tag() {
	# Resolve the latest release tag from the GitHub API without jq.
	fetch_stdout "https://api.github.com/repos/${REPO}/releases/latest" \
		| grep -m1 '"tag_name"' \
		| sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/'
}

verify_sha256() { # verify_sha256 <file> <expected>
	expected="$2"
	if have sha256sum; then
		actual="$(sha256sum "$1" | awk '{print $1}')"
	elif have shasum; then
		actual="$(shasum -a 256 "$1" | awk '{print $1}')"
	else
		log "warning: no sha256sum/shasum found, skipping checksum verification"
		return 0
	fi
	[ "$actual" = "$expected" ] || die "checksum mismatch for $(basename "$1") (expected $expected, got $actual)"
}

choose_bin_dir() {
	if [ -n "${KUBETIDY_BIN_DIR:-}" ]; then
		echo "$KUBETIDY_BIN_DIR"; return
	fi
	if [ -w /usr/local/bin ] 2>/dev/null; then
		echo /usr/local/bin
	else
		echo "${HOME}/.local/bin"
	fi
}

main() {
	os="$(detect_os)"
	arch="$(detect_arch)"

	tag="${KUBETIDY_VERSION:-$(latest_tag)}"
	[ -n "$tag" ] || die "could not determine the latest release tag (set KUBETIDY_VERSION)"
	version="${tag#v}" # archive names drop the leading v

	archive="${BINARY}_${version}_${os}_${arch}.tar.gz"
	base_url="https://github.com/${REPO}/releases/download/${tag}"

	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT

	log "downloading kubetidy ${tag} (${os}/${arch})..."
	fetch "${base_url}/${archive}" "${tmp}/${archive}"

	# Verify against the release checksums.txt when reachable (best-effort).
	if fetch "${base_url}/checksums.txt" "${tmp}/checksums.txt" 2>/dev/null; then
		expected="$(grep " ${archive}\$" "${tmp}/checksums.txt" | awk '{print $1}')"
		[ -n "$expected" ] && verify_sha256 "${tmp}/${archive}" "$expected"
	else
		log "warning: could not fetch checksums.txt, skipping verification"
	fi

	tar -xzf "${tmp}/${archive}" -C "$tmp"
	[ -f "${tmp}/${BINARY}" ] || die "archive did not contain the ${BINARY} binary"

	bin_dir="$(choose_bin_dir)"
	mkdir -p "$bin_dir"

	# Install kubetidy (use sudo only if the dir is not writable and sudo exists).
	if [ -w "$bin_dir" ]; then
		install -m 0755 "${tmp}/${BINARY}" "${bin_dir}/${BINARY}"
		[ -n "${KUBETIDY_NO_PLUGIN:-}" ] || install -m 0755 "${tmp}/${BINARY}" "${bin_dir}/kubectl-tidy"
	elif have sudo; then
		log "installing to ${bin_dir} (needs sudo)"
		sudo install -m 0755 "${tmp}/${BINARY}" "${bin_dir}/${BINARY}"
		[ -n "${KUBETIDY_NO_PLUGIN:-}" ] || sudo install -m 0755 "${tmp}/${BINARY}" "${bin_dir}/kubectl-tidy"
	else
		die "cannot write to ${bin_dir} and sudo is unavailable; set KUBETIDY_BIN_DIR to a writable dir"
	fi

	log "installed ${BINARY} to ${bin_dir}/${BINARY}"
	[ -n "${KUBETIDY_NO_PLUGIN:-}" ] || log "installed kubectl plugin to ${bin_dir}/kubectl-tidy"

	case ":${PATH}:" in
		*":${bin_dir}:"*) ;;
		*) log "note: ${bin_dir} is not on your PATH — add it, e.g. export PATH=\"${bin_dir}:\$PATH\"" ;;
	esac

	log ""
	log "try it:  ${BINARY} scan   (or:  kubectl tidy scan)"
}

main "$@"
