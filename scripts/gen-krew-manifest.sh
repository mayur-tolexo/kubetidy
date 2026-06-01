#!/usr/bin/env sh
# gen-krew-manifest.sh — render the krew plugin manifest for a release.
#
# It reads the goreleaser checksums file, emits one krew platform entry per kubetidy archive
# (linux/darwin tar.gz, windows zip) with the real sha256 and download URL, and substitutes
# __VERSION__ / __PLATFORMS__ in the template.
#
# Usage:
#   scripts/gen-krew-manifest.sh <tag> <checksums-file> [template] [output]
#
# Example:
#   scripts/gen-krew-manifest.sh v1.2.3 dist/checksums.txt .krew.yaml.tmpl dist/kubetidy.yaml
set -eu

REPO="mayur-tolexo/kubetidy"

TAG="${1:?usage: gen-krew-manifest.sh <tag> <checksums-file> [template] [output]}"
CHECKSUMS="${2:?missing checksums file}"
TEMPLATE="${3:-.krew.yaml.tmpl}"
OUTPUT="${4:-dist/kubetidy.yaml}"

[ -f "$CHECKSUMS" ] || { echo "checksums file not found: $CHECKSUMS" >&2; exit 1; }
[ -f "$TEMPLATE" ] || { echo "template not found: $TEMPLATE" >&2; exit 1; }

base_url="https://github.com/${REPO}/releases/download/${TAG}"

# Build the platforms YAML into a temp file, one entry per kubetidy_* archive.
platforms_file="$(mktemp)"
trap 'rm -f "$platforms_file"' EXIT

# checksums.txt lines look like: "<sha256>  kubetidy_1.2.3_linux_amd64.tar.gz"
while IFS= read -r line; do
	[ -n "$line" ] || continue
	sha="$(printf '%s\n' "$line" | awk '{print $1}')"
	file="$(printf '%s\n' "$line" | awk '{print $2}')"

	# Only archive files we know how to map (skip stray entries).
	case "$file" in
		kubetidy_*.tar.gz) bin="kubetidy"; stripped="${file%.tar.gz}" ;;
		kubetidy_*.zip)    bin="kubetidy.exe"; stripped="${file%.zip}" ;;
		*) continue ;;
	esac

	# stripped = kubetidy_<version>_<os>_<arch>
	arch="${stripped##*_}"
	rest="${stripped%_*}"
	os="${rest##*_}"

	case "$os" in linux|darwin|windows) ;; *) continue ;; esac
	case "$arch" in amd64|arm64) ;; *) continue ;; esac

	cat >>"$platforms_file" <<EOF
    - selector:
        matchLabels:
          os: ${os}
          arch: ${arch}
      uri: ${base_url}/${file}
      sha256: ${sha}
      bin: ${bin}
      files:
        - from: ${bin}
          to: .
        - from: LICENSE
          to: .
EOF
done <"$CHECKSUMS"

if [ ! -s "$platforms_file" ]; then
	echo "no kubetidy archives found in $CHECKSUMS" >&2
	exit 1
fi

mkdir -p "$(dirname "$OUTPUT")"

# Substitute __VERSION__, then splice the generated platforms block in place of __PLATFORMS__.
# Done with awk so multi-line platform YAML is inserted verbatim (no sed newline escaping).
awk -v ver="$TAG" -v pf="$platforms_file" '
	{ gsub(/__VERSION__/, ver) }
	/^__PLATFORMS__[[:space:]]*$/ {
		while ((getline pl < pf) > 0) print pl
		next
	}
	{ print }
' "$TEMPLATE" >"$OUTPUT"

echo "wrote $OUTPUT ($TAG)" >&2
