#!/usr/bin/env bash
set -euo pipefail

# Publish hey-cli to AUR
# Requires: GITHUB_TOKEN, AUR_KEY environment variables

VERSION="${GITHUB_REF_NAME#v}"
REPO="basecamp/hey-cli"

echo "Publishing hey-cli $VERSION to AUR..."

# Download source tarball and compute checksum
SOURCE_URL="https://github.com/$REPO/archive/v${VERSION}.tar.gz"
curl -fsSL "$SOURCE_URL" -o source.tar.gz
SHA256=$(sha256sum source.tar.gz | cut -d' ' -f1)
rm source.tar.gz

# Generate PKGBUILD
cat > PKGBUILD << EOF
# Maintainer: 37signals <support@37signals.com>
pkgname=hey-cli
pkgver=$VERSION
pkgrel=1
pkgdesc="CLI for HEY email"
arch=('x86_64' 'aarch64')
url="https://github.com/$REPO"
license=('MIT')
depends=('glibc')
makedepends=('go')
provides=('hey')
conflicts=('hey' 'hey-bin')
source=("\$pkgname-\$pkgver.tar.gz::https://github.com/$REPO/archive/v\$pkgver.tar.gz")
sha256sums=('$SHA256')
options=('!debug')

build() {
    cd "\$pkgname-\$pkgver"
    export CGO_CPPFLAGS="\${CPPFLAGS}"
    export CGO_CFLAGS="\${CFLAGS}"
    export CGO_CXXFLAGS="\${CXXFLAGS}"
    export CGO_LDFLAGS="\${LDFLAGS}"
    export GOFLAGS="-buildmode=pie -trimpath -mod=readonly -modcacherw"
    go build -ldflags "-s -w -X github.com/basecamp/hey-cli/internal/version.Version=\${pkgver}" -o hey ./cmd/hey

    # Generate completions
    ./hey completion bash > hey.bash
    ./hey completion zsh > hey.zsh
    ./hey completion fish > hey.fish
}

package() {
    cd "\$pkgname-\$pkgver"
    install -Dm755 hey "\$pkgdir/usr/bin/hey"
    install -Dm644 LICENSE.md "\$pkgdir/usr/share/licenses/\$pkgname/LICENSE.md"
    install -Dm644 hey.bash "\$pkgdir/usr/share/bash-completion/completions/hey"
    install -Dm644 hey.zsh "\$pkgdir/usr/share/zsh/site-functions/_hey"
    install -Dm644 hey.fish "\$pkgdir/usr/share/fish/vendor_completions.d/hey.fish"
}
EOF

# Generate .SRCINFO
cat > .SRCINFO << EOF
pkgbase = hey-cli
	pkgdesc = CLI for HEY email
	pkgver = $VERSION
	pkgrel = 1
	url = https://github.com/$REPO
	arch = x86_64
	arch = aarch64
	license = MIT
	makedepends = go
	depends = glibc
	provides = hey
	conflicts = hey
	conflicts = hey-bin
	options = !debug
	source = hey-cli-$VERSION.tar.gz::https://github.com/$REPO/archive/v$VERSION.tar.gz
	sha256sums = $SHA256

pkgname = hey-cli
EOF

# Clone AUR repo and push
mkdir -p ~/.ssh
echo "$AUR_KEY" > ~/.ssh/aur
chmod 600 ~/.ssh/aur
cat >> ~/.ssh/config << SSHEOF
Host aur.archlinux.org
    IdentityFile ~/.ssh/aur
    User aur
    StrictHostKeyChecking accept-new
SSHEOF

git clone ssh://aur@aur.archlinux.org/hey-cli.git aur-repo
cp PKGBUILD .SRCINFO aur-repo/
cd aur-repo
git config user.name "hey-release-bot"
git config user.email "hey-release-bot@users.noreply.github.com"
git add PKGBUILD .SRCINFO
if git diff --cached --quiet; then
  echo "AUR package already up to date for $VERSION"
else
  git commit -m "Update to $VERSION"
  git push
fi

echo "Published hey-cli $VERSION to AUR"
