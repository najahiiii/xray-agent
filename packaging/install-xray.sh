#!/usr/bin/env bash
set -euo pipefail

# Xray-core installer (latest from GitHub Releases)
# - Detects arch (x86_64/aarch64/armv7)
# - Downloads the latest non-prerelease asset
# - Verifies SHA256 using the .dgst file
# - Installs to /usr/local/bin/xray (and geoip/geosite)
# - Creates xray user, directories, sample config, and systemd service
# - Starts and enables systemd service
#
# Usage:
#   sudo bash install-xray.sh [--version v1.8.24] [--bin-dir /usr/local/bin] [--config /etc/xray/config.json]
# Env:
#   GITHUB_TOKEN=xxxx   (optional, to avoid rate limit)

REPO="XTLS/Xray-core"
BIN_DIR="/usr/local/bin"
CFG_PATH="/etc/xray/config.json"
VERSION=""
ARCH=""
SAMPLE_CFG_URL="https://gist.githubusercontent.com/najahiiii/04e3a094517f2a56c04263afdf60805f/raw/73a6115d6b2f34f0de918a32ac582661fce69d75/xray-config.json"
SERVICE_URL="https://gist.githubusercontent.com/najahiiii/04e3a094517f2a56c04263afdf60805f/raw/73a6115d6b2f34f0de918a32ac582661fce69d75/xray.service"

log() { echo -e "[+] $*"; }
err() { echo -e "[!] $*" >&2; }
need_root() { if [[ $EUID -ne 0 ]]; then err "Run as root"; exit 1; fi }

install_deps() {
  local required=(curl jq unzip)
  local missing=()
  for cmd in "${required[@]}"; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
      missing+=("$cmd")
    fi
  done
  if [[ ${#missing[@]} -eq 0 ]]; then
    log "All required dependencies already installed"
    return
  fi

  if command -v apt-get >/dev/null 2>&1; then
    apt-get update -y
    DEBIAN_FRONTEND=noninteractive apt-get install -y "${missing[@]}" ca-certificates
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y "${missing[@]}" ca-certificates || true
    update-ca-trust || true
  elif command -v yum >/dev/null 2>&1; then
    yum install -y "${missing[@]}" ca-certificates || true
    update-ca-trust || true
  elif command -v pacman >/dev/null 2>&1; then
    pacman -Sy --noconfirm "${missing[@]}" ca-certificates
  else
    err "Unsupported distro. Please install: ${missing[*]} ca-certificates"; exit 1
  fi
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --version) VERSION="$2"; shift 2;;
      --bin-dir) BIN_DIR="$2"; shift 2;;
      --config)  CFG_PATH="$2"; shift 2;;
      --arch)    ARCH="$2"; shift 2;;
      *) err "Unknown arg: $1"; exit 1;;
    esac
  done
}

detect_arch() {
  local u
  u=$(uname -m)
  case "$u" in
    x86_64|amd64) ARCH="linux-64";;
    aarch64|arm64) ARCH="linux-arm64-v8a";;
    armv7l|armhf|armv7) ARCH="linux-arm32-v7a";;
    *) err "Unsupported arch: $u"; exit 1;;
  esac
}

api_get() {
  local url="$1"
  local h=("-H" "Accept: application/vnd.github+json")
  if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    h+=("-H" "Authorization: Bearer $GITHUB_TOKEN")
  fi
  curl -fsSL "${h[@]}" "$url"
}

latest_release_json() {
  if [[ -n "$VERSION" ]]; then
    api_get "https://api.github.com/repos/$REPO/releases/tags/$VERSION"
  else
    api_get "https://api.github.com/repos/$REPO/releases/latest"
  fi
}

pick_asset_urls() {
  local arch="$1"
  local json="$2"
  ZIP_URL=$(echo "$json" | jq -r --arg a "$arch" '.assets[] | select(.name|test("^Xray-"+$a+"\\.zip$")) | .browser_download_url' | head -n1)
  DGST_URL=$(echo "$json" | jq -r --arg a "$arch" '.assets[] | select(.name|test("^Xray-"+$a+"\\.zip\\.dgst$")) | .browser_download_url' | head -n1)
  if [[ -z "$ZIP_URL" || -z "$DGST_URL" ]]; then
    err "Cannot find asset for arch=$arch. Check release page."; exit 1
  fi
}

verify_sha256() {
  local zip="$1" dgst_file="$2"
  local want
  want=$(grep -Eo '[A-Fa-f0-9]{64}' "$dgst_file" | head -n1)
  if [[ -z "$want" ]]; then err "SHA256 not found in .dgst"; exit 1; fi
  local got
  got=$(sha256sum "$zip" | awk '{print $1}')
  if [[ "$want" != "$got" ]]; then
    err "SHA256 mismatch! want=$want got=$got"; exit 1
  fi
}

create_work_dirs() {
  mkdir -p /etc/xray /var/log/xray /var/lib/xray /usr/local/share/xray
}

install_binary_and_data() {
  local tmpdir="$1"
  install -m 0755 "$tmpdir/xray" "$BIN_DIR/xray"
  # optional data files
  for f in geoip.dat geosite.dat; do
    if [[ -f "$tmpdir/$f" ]]; then
      install -m 0644 "$tmpdir/$f" "/usr/local/share/xray/$f"
    fi
  done
}

write_sample_config_if_absent() {
  if [[ -f "$CFG_PATH" ]]; then return; fi
  mkdir -p "$(dirname "$CFG_PATH")"
  log "Downloading sample config from gist"
  if ! curl -fsSL -o "$CFG_PATH" "$SAMPLE_CFG_URL"; then
    err "Failed to download sample config from $SAMPLE_CFG_URL"
    exit 1
  fi
  chmod 0644 "$CFG_PATH"
}

install_systemd_service() {
  local svc="/etc/systemd/system/xray.service"
  mkdir -p "$(dirname "$svc")"
  log "Downloading systemd service from gist"
  if ! curl -fsSL -o "$svc" "$SERVICE_URL"; then
    err "Failed to download service unit from $SERVICE_URL"
    exit 1
  fi
  systemctl daemon-reload
  systemctl enable --now xray
}

main() {
  need_root
  parse_args "$@"
  install_deps
  [[ -z "$ARCH" ]] && detect_arch
  log "Detect arch → $ARCH"
  json=$(latest_release_json)
  pick_asset_urls "$ARCH" "$json"
  log "ZIP:  $ZIP_URL"
  log "DGST: $DGST_URL"

  work=$(mktemp -d)
  trap 'rm -rf "$work"' EXIT
  curl -fsSL -o "$work/xray.zip" "$ZIP_URL"
  curl -fsSL -o "$work/xray.zip.dgst" "$DGST_URL"
  verify_sha256 "$work/xray.zip" "$work/xray.zip.dgst"
  log "SHA256 OK"
  unzip -q "$work/xray.zip" -d "$work/unzip"

  create_work_dirs
  install_binary_and_data "$work/unzip"
  write_sample_config_if_absent

  # test config once
  /usr/local/bin/xray -test -config "$CFG_PATH"

  install_systemd_service
  log "Xray installed at $BIN_DIR/xray"
  log "Config: $CFG_PATH (sample created if absent)"
  log "Logs: /var/log/xray/ | Data: /usr/local/share/xray/"
}

main "$@"
