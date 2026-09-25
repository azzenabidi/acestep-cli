#!/usr/bin/env sh
#
# acestep installer
#
#   curl -fsSL https://raw.githubusercontent.com/azzenabidi/acestep-cli/main/scripts/install.sh | sh
#
# Downloads the release binary for this platform and installs it to a directory
# on your PATH. Model weights and the engine itself are NOT installed here;
# run `acestep setup` for that.
#
# Environment:
#   ACESTEP_VERSION   release tag to install (default: latest)
#   ACESTEP_INSTALL   destination directory (default: /usr/local/bin, or
#                     ~/.local/bin when that is not writable)
#   ACESTEP_BIN_DIR   alternative name for ACESTEP_INSTALL
#   ACESTEP_RELEASES  base release URL (default: GitHub; override for a mirror)
#   NO_COLOR          set to any value to disable coloured output
#   VERBOSE           set to any value to see every command

set -eu

REPO="azzenabidi/acestep-cli"
RELEASES="${ACESTEP_RELEASES:-https://github.com/$REPO/releases}"

# --- output helpers ---------------------------------------------------------

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  BOLD="$(printf '\033[1m')"; DIM="$(printf '\033[2m')"
  RED="$(printf '\033[31m')"; GREEN="$(printf '\033[32m')"
  YELLOW="$(printf '\033[33m')"; RESET="$(printf '\033[0m')"
else
  BOLD=""; DIM=""; RED=""; GREEN=""; YELLOW=""; RESET=""
fi

step() { printf '%s==>%s %s\n' "$BOLD" "$RESET" "$*"; }
info() { printf '    %s%s%s\n' "$DIM" "$*" "$RESET"; }
ok()   { printf '%s  ok%s %s\n' "$GREEN" "$RESET" "$*"; }
warn() { printf '%swarn%s %s\n' "$YELLOW" "$RESET" "$*" >&2; }
die()  { printf '%serror%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

run() {
  [ -n "${VERBOSE:-}" ] && printf '    $ %s\n' "$*"
  "$@"
}

# --- platform detection -----------------------------------------------------

detect_platform() {
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m)

  case "$os" in
    linux)  os=Linux ;;
    darwin) os=Darwin ;;
    *)      die "unsupported operating system: $os" ;;
  esac

  case "$arch" in
    x86_64|amd64)  arch=x86_64 ;;
    arm64|aarch64) arch=arm64 ;;
    *)             die "unsupported architecture: $arch" ;;
  esac

  # GoReleaser only publishes amd64/arm64.
  printf '%s_%s' "$os" "$arch"
}

# --- checks -----------------------------------------------------------------

require() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"
}

# --- install ----------------------------------------------------------------

choose_dest() {
  dest="${ACESTEP_INSTALL:-${ACESTEP_BIN_DIR:-}}"
  if [ -n "$dest" ]; then
    printf '%s' "$dest"
    return
  fi
  if [ -w /usr/local/bin ] 2>/dev/null || [ "$(id -u)" = "0" ]; then
    printf '%s' /usr/local/bin
  else
    printf '%s' "$HOME/.local/bin"
  fi
}

main() {
  require curl
  require tar
  [ "$(uname -s)" = "Darwin" ] || command -v unzip >/dev/null 2>&1 || true

  version="${ACESTEP_VERSION:-latest}"
  platform=$(detect_platform)
  dest=$(choose_dest)

  step "Installing acestep ($platform)"

  if [ "$version" = "latest" ]; then
    info "resolving the latest release"
    # The release page redirects to .../tag/<version>. If there are no releases
    # at all there is no redirect, and url_effective stays as the input URL, so
    # the /tag/ component has to be checked rather than assumed.
    effective=$(curl -fsSL -o /dev/null -w '%{url_effective}' \
      "$RELEASES/latest" 2>/dev/null || true)
    case "$effective" in
      */tag/*) tag=${effective##*/tag/} ;;
      *)      die "could not find a published release at $RELEASES" ;;
    esac
    [ -n "$tag" ] || die "could not determine the latest release"
    case "$tag" in
      */*|*" "*|*[![:print:]]*) die "unexpected release tag: $tag" ;;
    esac
  else
    tag="$version"
  fi

  asset="acestep_${tag#v}_${platform}"
  case "$platform" in
    Linux_x86_64|Linux_arm64|Darwin_x86_64) ext=tar.gz ;;
    Darwin_arm64)                        ext=tar.gz ;;
    *)                                   ext=zip ;;
  esac
  url="$RELEASES/download/$tag/${asset}.${ext}"

  tmp=$(mktemp -d 2>/dev/null || mktemp -d -t acestep)
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" EXIT INT TERM

  info "downloading $asset.$ext"
  run curl -fL --progress-bar -o "$tmp/asset.$ext" "$url" \
    || die "download failed: $url"

  if command -v shasum >/dev/null 2>&1; then
    if run curl -fsSL -o "$tmp/checksums.txt" "$RELEASES/download/$tag/checksums.txt"; then
      expected=$(grep " ${asset}\.$ext\$" "$tmp/checksums.txt" | cut -d' ' -f1 || true)
      actual=$(shasum -a 256 "$tmp/asset.$ext" | cut -d' ' -f1)
      if [ -n "$expected" ]; then
        [ "$expected" = "$actual" ] || die "checksum mismatch for $asset.$ext"
        ok "checksum verified"
      else
        warn "no checksum published for $asset.$ext; skipped verification"
      fi
    else
      warn "could not fetch checksums.txt; skipped verification"
    fi
  else
    warn "shasum not available; skipped checksum verification"
  fi

  info "extracting"
  case "$ext" in
    tar.gz) run tar -xzf "$tmp/asset.$ext" -C "$tmp" acestep ;;
    zip)    run unzip -o -q "$tmp/asset.$ext" acestep -d "$tmp" ;;
  esac
  [ -f "$tmp/acestep" ] || die "the archive did not contain an 'acestep' binary"
  chmod +x "$tmp/acestep"

  mkdir -p "$dest"
  [ -w "$dest" ] || die "no write permission for $dest (try ACESTEP_INSTALL=...)"
  info "installing to $dest/acestep"
  run mv -f "$tmp/acestep" "$dest/acestep"

  ok "installed $tag -> $dest/acestep"

  case ":$PATH:" in
    *":$dest:"*) ;;
    *) warn "$dest is not on your PATH"
       warn "add this to your shell profile:  export PATH=\"$dest:\$PATH\"" ;;
  esac

  printf '\n'
  step "Next"
  printf '    %s1.%s build the engine and download the models (several GB):\n       acestep setup --models standard\n' "$BOLD" "$RESET"
  printf '    %s2.%s check the installation:\n       acestep doctor\n' "$BOLD" "$RESET"
  printf '    %s3.%s generate a track:\n       acestep generate -p "lofi beats" -d 30\n' "$BOLD" "$RESET"
  printf '\n'
  info "pick a bundle that fits your hardware; see docs/HARDWARE_GUIDE.md"
  info "the engine is not installed by this script -- `acestep setup` builds it"
}

main "$@"
