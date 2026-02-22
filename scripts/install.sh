#!/usr/bin/env sh
set -eu

REPO="${MINDCLI_REPO:-jankowtf/mindcli}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${VERSION:-}"
DOWNLOAD_URL="${MINDCLI_DOWNLOAD_URL:-}"

detect_os() {
  case "$(uname -s)" in
    Darwin) echo "darwin" ;;
    Linux) echo "linux" ;;
    *)
      echo "unsupported OS: $(uname -s)" >&2
      exit 1
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *)
      echo "unsupported architecture: $(uname -m)" >&2
      exit 1
      ;;
  esac
}

latest_tag() {
  curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
    sed -n 's/.*"tag_name":[[:space:]]*"\(v[^"]*\)".*/\1/p' |
    head -n1
}

if [ -z "${DOWNLOAD_URL}" ] && [ -z "${VERSION}" ]; then
  VERSION="$(latest_tag)"
fi

if [ -z "${DOWNLOAD_URL}" ] && [ -z "${VERSION}" ]; then
  echo "could not determine release version" >&2
  exit 1
fi

OS="$(detect_os)"
ARCH="$(detect_arch)"
if [ -z "${DOWNLOAD_URL}" ]; then
  VERSION_NO_V="${VERSION#v}"
  ARCHIVE="mindcli_${VERSION_NO_V}_${OS}_${ARCH}.tar.gz"
  URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"
else
  ARCHIVE="$(basename "${DOWNLOAD_URL}")"
  URL="${DOWNLOAD_URL}"
fi

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT INT TERM

echo "Downloading ${URL}"
curl -fsSL "${URL}" -o "${TMP_DIR}/${ARCHIVE}"
tar -xzf "${TMP_DIR}/${ARCHIVE}" -C "${TMP_DIR}"

if [ ! -f "${TMP_DIR}/mindcli" ]; then
  echo "downloaded archive does not contain mindcli binary" >&2
  exit 1
fi

install_bin() {
  target_dir="$1"
  mkdir -p "${target_dir}"
  install -m 0755 "${TMP_DIR}/mindcli" "${target_dir}/mindcli"
  echo "Installed mindcli to ${target_dir}/mindcli"
}

if [ -d "${INSTALL_DIR}" ] && [ -w "${INSTALL_DIR}" ]; then
  install_bin "${INSTALL_DIR}"
elif [ ! -e "${INSTALL_DIR}" ] && [ -w "$(dirname "${INSTALL_DIR}")" ]; then
  install_bin "${INSTALL_DIR}"
elif command -v sudo >/dev/null 2>&1; then
  sudo mkdir -p "${INSTALL_DIR}"
  sudo install -m 0755 "${TMP_DIR}/mindcli" "${INSTALL_DIR}/mindcli"
  echo "Installed mindcli to ${INSTALL_DIR}/mindcli"
else
  FALLBACK_DIR="${HOME}/.local/bin"
  install_bin "${FALLBACK_DIR}"
  echo "Add ${FALLBACK_DIR} to PATH if it is not already present."
fi
