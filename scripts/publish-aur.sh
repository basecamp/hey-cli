#!/usr/bin/env bash
# Publish the binary package to AUR as hey-cli.
# Called from the release workflow after GoReleaser uploads assets, and from
# the aur-publish recovery workflow for versions whose release-time publish
# could not run.
#
# Derives the PKGBUILD entirely from the published GitHub release assets and
# no-ops when the AUR copy is already current, so re-running it against an
# already-published version is safe and idempotent.
#
# Expects SSH access to aur.archlinux.org and a git identity to already be
# configured by the caller.
#
# Usage: scripts/publish-aur.sh <version>
set -euo pipefail

VERSION="${1:?usage: publish-aur.sh <version>}"
PKGNAME="hey-cli"
AUR_REPO="ssh://aur@aur.archlinux.org/${PKGNAME}.git"

# Compute checksums from the GitHub release assets
base_url="https://github.com/basecamp/hey-cli/releases/download/v${VERSION}"
sha_x86=$(curl -sL "${base_url}/checksums.txt" | grep "linux_amd64\.tar\.gz$" | awk '{print $1}')
sha_arm=$(curl -sL "${base_url}/checksums.txt" | grep "linux_arm64\.tar\.gz$" | awk '{print $1}')

if [ -z "$sha_x86" ] || [ -z "$sha_arm" ]; then
  echo "ERROR: could not find linux checksums in release assets" >&2
  exit 1
fi

# Generate PKGBUILD
pkgbuild=$(cat <<PKGBUILD
# Maintainer: 37signals <support@37signals.com>
pkgname=${PKGNAME}
pkgver=${VERSION}
pkgrel=1
pkgdesc="CLI for HEY email"
arch=('x86_64' 'aarch64')
url="https://github.com/basecamp/hey-cli"
license=('MIT')
provides=('hey')
conflicts=('hey' 'hey-bin')
optdepends=(
  'bash-completion: for bash shell completions'
  'zsh: for zsh shell completions'
  'fish: for fish shell completions'
)
source_x86_64=("${base_url}/hey_\${pkgver}_linux_amd64.tar.gz")
source_aarch64=("${base_url}/hey_\${pkgver}_linux_arm64.tar.gz")
sha256sums_x86_64=('${sha_x86}')
sha256sums_aarch64=('${sha_arm}')

package() {
  install -Dm755 "hey" "\${pkgdir}/usr/bin/hey"
  install -Dm644 "LICENSE.md" "\${pkgdir}/usr/share/licenses/\${pkgname}/LICENSE.md"
  install -Dm644 "completions/hey.bash" "\${pkgdir}/usr/share/bash-completion/completions/hey"
  install -Dm644 "completions/_hey" "\${pkgdir}/usr/share/zsh/site-functions/_hey"
  install -Dm644 "completions/hey.fish" "\${pkgdir}/usr/share/fish/vendor_completions.d/hey.fish"
}
PKGBUILD
)

# Generate .SRCINFO
srcinfo=$(cat <<SRCINFO
pkgbase = ${PKGNAME}
	pkgdesc = CLI for HEY email
	pkgver = ${VERSION}
	pkgrel = 1
	url = https://github.com/basecamp/hey-cli
	arch = x86_64
	arch = aarch64
	license = MIT
	provides = hey
	conflicts = hey
	conflicts = hey-bin
	optdepends = bash-completion: for bash shell completions
	optdepends = zsh: for zsh shell completions
	optdepends = fish: for fish shell completions
	source_x86_64 = ${base_url}/hey_${VERSION}_linux_amd64.tar.gz
	sha256sums_x86_64 = ${sha_x86}
	source_aarch64 = ${base_url}/hey_${VERSION}_linux_arm64.tar.gz
	sha256sums_aarch64 = ${sha_arm}

pkgname = ${PKGNAME}
SRCINFO
)

# Clone, update, push
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT

git clone "$AUR_REPO" "$workdir/aur"
cd "$workdir/aur"

# Never move the AUR backwards. The release workflow's concurrency group
# serializes publishes but does not order them by version, so a re-run of an
# older release's job can land after a newer version shipped. The just-cloned
# PKGBUILD is the authoritative current state — read it rather than the AUR
# RPC, which can lag. Exiting 0 keeps an out-of-order automatic run from
# failing loudly over a package that is already correct; the recovery workflow
# still rejects a downgrade dispatch with a hard error before reaching here.
if [ -f PKGBUILD ]; then
  current=$(sed -n 's/^pkgver=//p' PKGBUILD)
  currel=$(sed -n 's/^pkgrel=//p' PKGBUILD)
  if [ -n "$current" ]; then
    if [ "$current" = "$VERSION" ]; then
      # pkgrel=1 is hardcoded below, so republishing over a higher revision
      # would silently discard an AUR-side packaging fix.
      if [ "${currel:-1}" != "1" ]; then
        echo "AUR already has ${current}-${currel}; refusing to clobber the packaging revision"
        exit 0
      fi
    else
      newest=$(printf '%s\n%s\n' "$current" "$VERSION" | sort -V | tail -1)
      if [ "$newest" != "$VERSION" ]; then
        echo "AUR already at ${current}, newer than ${VERSION}; nothing to do"
        exit 0
      fi
    fi
  fi
fi

echo "$pkgbuild" > PKGBUILD
echo "$srcinfo" > .SRCINFO

git add PKGBUILD .SRCINFO
if git diff --cached --quiet; then
  echo "AUR already up to date"
  exit 0
fi

git commit -m "Update to v${VERSION}"
git push origin master
echo "Published ${PKGNAME} v${VERSION} to AUR"
