#!/usr/bin/env sh
set -eu

TMP_DIR="$(mktemp -d)"
cleanup() {
  chmod -R u+w "${TMP_DIR}" 2>/dev/null || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT INT TERM

export GOCACHE="${TMP_DIR}/.gocache"
export GOMODCACHE="${TMP_DIR}/.gomodcache"
mkdir -p "${GOCACHE}" "${GOMODCACHE}"

VERSION="0.0.0-ci"
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH_RAW="$(uname -m)"
case "${ARCH_RAW}" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)
    echo "unsupported architecture: ${ARCH_RAW}" >&2
    exit 1
    ;;
esac

ARCHIVE="mindcli_${VERSION}_${OS}_${ARCH}.tar.gz"

echo "Building test binary..."
CGO_ENABLED=1 GOOS="${OS}" GOARCH="${ARCH}" go build -ldflags "-s -w -X main.version=${VERSION}" -o "${TMP_DIR}/mindcli" ./cmd/mindcli

echo "Creating release-style archive ${ARCHIVE}..."
tar -czf "${TMP_DIR}/${ARCHIVE}" -C "${TMP_DIR}" mindcli

echo "Testing install script with local archive..."
INSTALL_DIR="${TMP_DIR}/bin" MINDCLI_DOWNLOAD_URL="file://${TMP_DIR}/${ARCHIVE}" ./scripts/install.sh

"${TMP_DIR}/bin/mindcli" version >/dev/null
echo "Release smoke test passed."
