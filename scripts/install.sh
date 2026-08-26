#!/usr/bin/env bash
# install.sh - Install the hey CLI
#
# Usage:
#   curl -fsSL https://hey.com/install-cli | bash
#
# Options (via environment):
#   HEY_BIN_DIR    Where to install the binary
#                  (default: ~/bin if on PATH, else ~/.local/bin if on PATH;
#                   otherwise ~/bin on Windows, ~/.local/bin elsewhere)
#   HEY_VERSION    Specific version to install (default: latest)
#   HEY_SKIP_SETUP Set to 1 to skip the interactive setup wizard after install
#                  (still runs `hey setup agents` to install the agent skill
#                  and connect coding agents without prompting)
#   HEY_SETUP_AGENT
#                  Which coding agent(s) `setup agents` connects:
#                  claude | codex | grok | all | none (default: auto-detect a single
#                  agent; several detected connects none and lists them)
#
# Verification: the SHA-256 checksum is always verified against the release's
# checksums.txt. When cosign is installed, checksums.txt is additionally
# verified against its keyless Sigstore bundle (checksums.txt.bundle), pinned
# to this repository's release workflow identity.

set -euo pipefail

REPO="basecamp/hey-cli"
BIN_DIR="${HEY_BIN_DIR:-}"
VERSION="${HEY_VERSION:-}"
CURL_SCHANNEL_FALLBACK_FLAG=""
CURL_LAST_ERROR=""
CURL_FALLBACK_NOTED=0

# Color helpers — respect NO_COLOR (https://no-color.org)
if [[ -z "${NO_COLOR:-}" ]] && [[ -t 1 ]]; then
  bold()  { printf '\033[1m%s\033[0m' "$1"; }
  green() { printf '\033[32m%s\033[0m' "$1"; }
  red()   { printf '\033[31m%s\033[0m' "$1"; }
  dim()   { printf '\033[2m%s\033[0m' "$1"; }
else
  bold()  { printf '%s' "$1"; }
  green() { printf '%s' "$1"; }
  red()   { printf '%s' "$1"; }
  dim()   { printf '%s' "$1"; }
fi

info()  { echo "  $(green "✓") $1"; }
step()  { echo "  $(bold "→") $1"; }
error() { echo "  $(red "✗ ERROR:") $1" >&2; exit 1; }

find_sha256_cmd() {
  if command -v sha256sum &>/dev/null; then
    echo "sha256sum"
  elif command -v shasum &>/dev/null; then
    echo "shasum -a 256"
  elif command -v sha256 &>/dev/null; then
    # FreeBSD/OpenBSD base system; -q prints the bare digest.
    echo "sha256 -q"
  else
    error "No SHA256 tool found (need sha256sum, shasum or sha256)"
  fi
}

path_contains_dir() {
  local dir="$1"
  [[ ":$PATH:" == *":$dir:"* ]]
}

default_bin_dir() {
  local platform="$1"

  if path_contains_dir "$HOME/bin"; then
    echo "$HOME/bin"
    return 0
  fi

  if path_contains_dir "$HOME/.local/bin"; then
    echo "$HOME/.local/bin"
    return 0
  fi

  if [[ "$platform" == windows_* ]]; then
    echo "$HOME/bin"
  else
    echo "$HOME/.local/bin"
  fi
}

# detect_platform prints "<os>_<arch>" in the exact shape the release archives
# use. macOS is per-arch (darwin_amd64, darwin_arm64): no universal
# "darwin_all" archive is built, and requesting one 404s.
detect_platform() {
  local os arch

  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$os" in
    darwin) os="darwin" ;;
    linux) os="linux" ;;
    freebsd) os="freebsd" ;;
    openbsd) os="openbsd" ;;
    mingw*|msys*|cygwin*) os="windows" ;;
    *) error "Unsupported OS: $os" ;;
  esac

  arch=$(uname -m)
  case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) error "Unsupported architecture: $arch" ;;
  esac

  echo "${os}_${arch}"
}

detect_curl_fallback() {
  local version_output help_output

  version_output=$(curl --version 2>/dev/null || true)
  if [[ "$version_output" != *[Ss]channel* ]]; then
    return 0
  fi

  help_output=$(curl --help all 2>/dev/null || true)
  if [[ "$help_output" == *"--ssl-revoke-best-effort"* ]]; then
    CURL_SCHANNEL_FALLBACK_FLAG="--ssl-revoke-best-effort"
  elif [[ "$help_output" == *"--ssl-no-revoke"* ]]; then
    CURL_SCHANNEL_FALLBACK_FLAG="--ssl-no-revoke"
  fi
}

curl_run() {
  # --show-error guarantees curl writes errors to stderr even if a future caller
  # passes -s without -S. The Schannel revocation detection below depends on
  # finding CRYPT_E_NO_REVOCATION_CHECK in stderr; without --show-error a -s
  # caller would silently lose the fallback.
  local err_file status err
  err_file=$(mktemp "${TMPDIR:-/tmp}/hey-curl.XXXXXX")

  if curl --show-error "$@" 2>"$err_file"; then
    rm -f "$err_file"
    CURL_LAST_ERROR=""
    return 0
  else
    status=$?
  fi

  err=$(<"$err_file")
  rm -f "$err_file"

  if [[ $status -ne 0 ]] && [[ -n "$CURL_SCHANNEL_FALLBACK_FLAG" ]] && [[ "$err" == *"CRYPT_E_NO_REVOCATION_CHECK"* ]]; then
    if [[ $CURL_FALLBACK_NOTED -eq 0 ]]; then
      echo "  $(bold "→") Windows certificate revocation checks are unavailable; retrying curl with ${CURL_SCHANNEL_FALLBACK_FLAG}" >&2
      CURL_FALLBACK_NOTED=1
    fi

    err_file=$(mktemp "${TMPDIR:-/tmp}/hey-curl.XXXXXX")
    if curl --show-error "$CURL_SCHANNEL_FALLBACK_FLAG" "$@" 2>"$err_file"; then
      rm -f "$err_file"
      CURL_LAST_ERROR=""
      return 0
    else
      status=$?
    fi

    err=$(<"$err_file")
    rm -f "$err_file"
  fi

  CURL_LAST_ERROR="$err"
  return "$status"
}

get_latest_version() {
  local url version api_json

  # Follow the releases/latest redirect to get the version from the final URL.
  # Avoids the GitHub API (no rate limiting) and grep/sed (better Windows compat).
  if url=$(curl_run -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest"); then
    version="${url##*/}"
    version="${version#v}"
    if [[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
      echo "$version"
      return 0
    fi
  fi

  # Fallback to the GitHub API if redirect parsing fails. Whitespace-tolerant
  # regex so a future GitHub format change (pretty-print, extra spaces) doesn't
  # silently break the fallback. Pure bash so no GNU-awk dependency.
  if api_json=$(curl_run -fsSL -H 'Accept: application/vnd.github+json' -H 'User-Agent: hey-cli-installer' "https://api.github.com/repos/${REPO}/releases/latest"); then
    if [[ $api_json =~ \"tag_name\"[[:space:]]*:[[:space:]]*\"v?([^\"]+)\" ]]; then
      version="${BASH_REMATCH[1]}"
      if [[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
        echo "$version"
        return 0
      fi
    fi
  fi

  error "Could not determine latest version. ${CURL_LAST_ERROR:+curl said: ${CURL_LAST_ERROR}. }If native Windows curl fails, try Scoop or PowerShell. If using Git Bash, try /usr/bin/curl instead."
}

# cosign_version prints the installed cosign's version (e.g. "v2.6.0"), empty
# when it can't be determined. The trailing `|| true` is load-bearing: under
# `set -euo pipefail` a broken cosign (nonzero `cosign version`) would
# otherwise abort the whole installer from inside the caller's command
# substitution — an unusable cosign must degrade to warn-and-skip, not abort.
cosign_version() {
  cosign version 2>/dev/null | awk -F': *' '/^GitVersion/ {print $2; exit}' || true
}

# cosign_bundle_support decides how to verify the release's Sigstore bundle,
# which is the new (protobuf, v0.3+json) format. Prints the extra verify-blob
# flag to use ("" for none) and returns 0 when verification can proceed:
#   v3+          → new-format parsing is the default; no flag
#   v2.6 – v2.x  → needs --new-bundle-format=true (v2.x defaults it to false)
#   < v2.6       → cannot verify (v2.4 chokes on the bundle's tlog key type,
#                  v2.2 lacks the flag entirely); caller warns and skips
cosign_bundle_support() {
  local version="$1" major minor

  if [[ "$version" =~ ^v?([0-9]+)\.([0-9]+)\. ]]; then
    major="${BASH_REMATCH[1]}"
    minor="${BASH_REMATCH[2]}"
  else
    return 1
  fi

  if (( major >= 3 )); then
    echo ""
  elif (( major == 2 && minor >= 6 )); then
    echo "--new-bundle-format=true"
  else
    return 1
  fi
}

verify_checksums() {
  local version="$1"
  local tmp_dir="$2"
  local archive_name="$3"
  local base_url="https://github.com/${REPO}/releases/download/v${version}"
  step "Verifying checksums..."

  if ! curl_run -fsSL "${base_url}/checksums.txt" -o "${tmp_dir}/checksums.txt"; then
    error "Failed to download checksums.txt${CURL_LAST_ERROR:+ (${CURL_LAST_ERROR})}"
  fi

  # Verify SHA256 checksum of the downloaded archive
  local expected actual
  expected=$(awk -v f="$archive_name" '$2 == f || $2 == ("*" f) {print $1; exit}' "${tmp_dir}/checksums.txt")
  actual=$(cd "$tmp_dir" && $(find_sha256_cmd) "$archive_name" | awk '{print $1}')
  [[ -n "$expected" && "$expected" == "$actual" ]]  \
    || error "Checksum verification failed for $archive_name"

  info "Checksum verified"

  # If cosign is available and understands the bundle format, verify the signature
  if command -v cosign &>/dev/null; then
    local cosign_ver bundle_flag
    cosign_ver=$(cosign_version)
    if bundle_flag=$(cosign_bundle_support "$cosign_ver"); then
      step "Verifying cosign signature..."

      if ! curl_run -fsSL "${base_url}/checksums.txt.bundle" -o "${tmp_dir}/checksums.txt.bundle"; then
        error "Failed to download checksums.txt.bundle${CURL_LAST_ERROR:+ (${CURL_LAST_ERROR})}"
      fi

      local -a cosign_args=(verify-blob --bundle "${tmp_dir}/checksums.txt.bundle")
      if [[ -n "$bundle_flag" ]]; then
        cosign_args+=("$bundle_flag")
      fi
      cosign_args+=( \
        --certificate-identity "https://github.com/basecamp/hey-cli/.github/workflows/release.yml@refs/tags/v${version}" \
        --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
        "${tmp_dir}/checksums.txt")

      cosign "${cosign_args[@]}" \
        || error "Cosign signature verification failed"

      info "Signature verified"
    else
      step "Skipping signature verification: cosign ${cosign_ver:-unknown} can't verify this release's bundle format (need cosign >= 2.6)"
    fi
  fi
}

download_binary() {
  local version="$1"
  local platform="$2"
  local tmp_dir="$3"
  local url archive_name ext

  # Determine archive extension
  if [[ "$platform" == windows_* ]]; then
    ext="zip"
  else
    ext="tar.gz"
  fi

  archive_name="hey_${version}_${platform}.${ext}"
  url="https://github.com/${REPO}/releases/download/v${version}/${archive_name}"

  step "Downloading hey v${version} for ${platform}..."

  if ! curl_run -fsSL "$url" -o "${tmp_dir}/${archive_name}"; then
    error "Failed to download from $url${CURL_LAST_ERROR:+ (${CURL_LAST_ERROR})}"
  fi

  # Verify integrity before extraction
  verify_checksums "$version" "$tmp_dir" "$archive_name"

  # Extract binary
  step "Extracting..."
  if [[ "$ext" == "zip" ]]; then
    unzip -q "${tmp_dir}/${archive_name}" -d "$tmp_dir"
  else
    tar -xzf "${tmp_dir}/${archive_name}" -C "$tmp_dir"
  fi

  # Find and install binary
  local binary_name="hey"
  if [[ "$platform" == windows_* ]]; then
    binary_name="hey.exe"
  fi

  if [[ ! -f "${tmp_dir}/${binary_name}" ]]; then
    error "Binary not found in archive"
  fi

  mkdir -p "$BIN_DIR"
  mv "${tmp_dir}/${binary_name}" "$BIN_DIR/"
  chmod +x "$BIN_DIR/$binary_name"

  info "Installed hey to $BIN_DIR/$binary_name"
}

setup_path() {
  # Check if BIN_DIR is in PATH
  if [[ ":$PATH:" == *":$BIN_DIR:"* ]]; then
    return 0
  fi

  step "Adding $BIN_DIR to PATH"

  local shell_rc=""
  local path_line="export PATH=\"$BIN_DIR:\$PATH\""
  case "${SHELL:-}" in
    */zsh)  shell_rc="$HOME/.zshrc" ;;
    # macOS terminals start bash as a login shell, which reads ~/.bash_profile
    # and not ~/.bashrc. Linux login shells (consoles, SSH) reach ~/.bashrc
    # through the distros' stock profile files.
    */bash) if [[ "$(uname -s)" == "Darwin" ]]; then
              shell_rc="$HOME/.bash_profile"
            else
              shell_rc="$HOME/.bashrc"
            fi ;;
    # fish sources neither ~/.profile nor Bourne syntax; fish_add_path
    # (fish >= 3.2) persists the entry and is idempotent across runs.
    */fish) shell_rc="${XDG_CONFIG_HOME:-$HOME/.config}/fish/config.fish"
            path_line="fish_add_path \"$BIN_DIR\"" ;;
    *)      shell_rc="$HOME/.profile" ;;
  esac

  if [[ -f "$shell_rc" ]] && grep -qF "$BIN_DIR" "$shell_rc" 2>/dev/null; then
    info "PATH already configured in $shell_rc"
  else
    mkdir -p "$(dirname "$shell_rc")"
    {
      echo ""
      echo "# Added by hey installer"
      echo "$path_line"
    } >> "$shell_rc"
    info "Added to $shell_rc"
    info "Run: source $shell_rc"
  fi
}

verify_install() {
  local platform="$1"
  local binary_name="hey"
  if [[ "$platform" == windows_* ]]; then
    binary_name="hey.exe"
  fi

  local installed_version err_file
  err_file=$(mktemp)
  if installed_version=$("$BIN_DIR/$binary_name" --version 2>"$err_file"); then
    rm -f "$err_file"
    info "$(green "${installed_version} installed")"
    return 0
  fi

  local run_error
  run_error=$(cat "$err_file")
  rm -f "$err_file"

  local detail="Installation failed - hey not working"
  if [[ -n "$run_error" ]]; then
    detail="$detail: $run_error"
  fi
  if [[ "$platform" == windows_* ]]; then
    detail="$detail
  Windows may have blocked the executable: Smart App Control only runs code-signed binaries.
  Either install inside WSL2 (curl -fsSL https://hey.com/install-cli | bash) or see
  https://github.com/basecamp/hey-cli#windows-smart-app-control-and-smartscreen"
  fi
  error "$detail"
}

show_banner() {
  echo ""
  echo "  $(bold "HEY CLI")"
  echo ""
}

main() {
  show_banner

  # Check for curl
  if ! command -v curl &>/dev/null; then
    error "curl is required but not installed"
  fi

  local platform version tmp_dir
  platform=$(detect_platform)
  detect_curl_fallback

  if [[ -z "$BIN_DIR" ]]; then
    BIN_DIR=$(default_bin_dir "$platform")
  fi

  if [[ -n "$VERSION" ]]; then
    version="$VERSION"
    if [[ ! $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
      error "Invalid version '${version}'. Expected semver format (e.g. 1.2.3 or 1.2.3-rc.1)."
    fi
  else
    version=$(get_latest_version)
  fi

  tmp_dir=$(mktemp -d)
  # Expand now on purpose: tmp_dir is local to main and gone by the time the
  # EXIT trap fires.
  # shellcheck disable=SC2064
  trap "rm -rf '${tmp_dir}'" EXIT

  local binary_name="hey"
  if [[ "$platform" == windows_* ]]; then
    binary_name="hey.exe"
  fi

  download_binary "$version" "$platform" "$tmp_dir"
  setup_path
  verify_install "$platform"

  echo ""

  # Hand off to the setup wizard only when stdin and stdout are a terminal and
  # it was not explicitly skipped. Non-interactive environments (CI, piped
  # input, coding agents like Claude Code or Codex) get the agent skill
  # installed and a best-effort agent connection via `setup agents`, plus
  # next-step instructions — the wizard prompts, and a prompt without a
  # terminal would hang.
  if [[ "${HEY_SKIP_SETUP:-}" == "1" ]]; then
    step "Skipping setup wizard (HEY_SKIP_SETUP=1)"
    post_install_setup "$binary_name"
    print_next_steps
  elif [[ -t 0 ]] && [[ -t 1 ]]; then
    # Setup is optional: a declined OAuth, a timeout or a busy callback port
    # must not turn an already-successful install into a failure under set -e.
    if ! "$BIN_DIR/$binary_name" setup; then
      echo ""
      echo "  Setup didn't finish — hey is installed; run it again any time:"
      print_next_steps
    fi
  else
    info "Skipping interactive setup (no terminal detected)."
    post_install_setup "$binary_name"
    print_next_steps
  fi
}

print_next_steps() {
  echo ""
  echo "  Next steps:"
  echo "    $(bold "hey auth login")    Authenticate with HEY"
  echo "    $(bold "hey setup")         Run the setup wizard"
  echo "    $(bold "hey --help")        See what you can do"
  echo ""
}

# binary_supports_setup_agents reports whether the installed binary exposes the
# `setup agents` subcommand. The hosted install.sh from main can outrun the
# latest release, so we probe rather than assume.
binary_supports_setup_agents() {
  "$1" setup --help 2>/dev/null | grep -qE '^[[:space:]]+agents[[:space:]]'
}

# post_install_setup connects coding agents without prompting, but only when
# the installed binary has the ownership-aware `setup agents` (it honors
# HEY_SETUP_AGENT itself: claude|codex|grok|all|none, unset = auto-detect one).
# The jq selector keeps the machine-readable command's envelope out of the
# human-facing installer while preserving its concise outcome.
#
# Releases that predate `setup agents` also predate the ownership gate on
# `skill install` — their legacy implementation overwrites whatever occupies
# the skill paths — so the installer never invokes an old binary's setup or
# skill commands. The printed next steps are the fallback: setup belongs to
# the user, on a binary that refuses to destroy their files.
#
# The real invocation carries HEY_NO_KEYRING=1, per-command rather than
# exported: it never touches credentials, and a locked headless keychain
# (CI, ssh) could otherwise block the keyring probe forever. The
# `setup --help` capability probe stays bare — help short-circuits first.
post_install_setup() {
  local binary_name="$1"
  local bin="$BIN_DIR/$binary_name"
  local summary

  if binary_supports_setup_agents "$bin"; then
    if summary=$(HEY_NO_KEYRING=1 "$bin" setup agents --jq '.summary' 2>/dev/null); then
      if [[ -n "$summary" ]]; then
        info "$summary"
      fi
    fi
  fi
}

# Guard so sourcing the script (e.g. from tests) doesn't run the installer.
# The if-form is required: `[[ … ]] && main` returns 1 when sourced, which
# trips `set -e` in the sourcing shell. The `:-$0` fallback is also
# required: bash executing from stdin (curl | bash) or -c leaves BASH_SOURCE
# unset, and the bare expansion aborts under `set -u`.
if [[ "${BASH_SOURCE[0]:-$0}" == "$0" ]]; then
  main "$@"
fi
