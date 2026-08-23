{ lib, fetchurl, go_1_27 }:

# The Go toolchain shared by the package build and the dev shell.
#
# go.mod's `go` directive is the floor; nixpkgs-unstable lagged it at 1.27rc3
# when go.mod moved to 1.27.0, so the toolchain is rebuilt from the upstream
# source tarball until nixpkgs catches up. The override is conditional and
# drops itself once go_1_27 reaches 1.27.0 — delete this file's override and
# return go_1_27 directly after a `nix flake update` that makes
# `lib.versionOlder` false. Keep minGo in step with go.mod: a toolchain bump
# that outpaces flake.lock breaks the build without touching go.mod or go.sum,
# which is why the nix-build CI job runs on every go.mod change and on tags.
#
# Both consumers must use this one derivation. The dev shell once listed
# the nixpkgs Go directly, which handed `nix develop` a Go older than go.mod's
# directive — `make build` then failed under GOTOOLCHAIN=local or offline.
let
  minGo = "1.27.0";
in
if lib.versionOlder go_1_27.version minGo
then go_1_27.overrideAttrs (_: {
  version = minGo;
  src = fetchurl {
    url = "https://go.dev/dl/go${minGo}.src.tar.gz";
    hash = "sha256-cAJAPXzERSnvbSb2mkSBgmM5Xq18FsBaWAiuBH6+sOU=";
  };
})
else go_1_27
