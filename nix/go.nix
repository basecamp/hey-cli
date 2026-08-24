{ go_1_26 }:

# The Go toolchain shared by the package build and the dev shell.
#
# Both consumers use this one derivation so the package and `nix develop`
# cannot drift. flake.lock temporarily selects a NixOS 26.05 snapshot because
# nixpkgs-unstable still carried Go 1.26.5 when 1.26.6 landed; a normal lock
# update can return to unstable once it catches up. The nix-build CI job covers
# every go.mod change and every release tag, so a regression fails closed.
go_1_26
