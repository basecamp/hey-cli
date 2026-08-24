{ go_1_27 }:

# The Go toolchain shared by the package build and the dev shell.
#
# Both consumers use this one derivation so the package and `nix develop`
# cannot drift. Keep flake.lock new enough that go_1_27 satisfies go.mod;
# the nix-build CI job covers every go.mod change and every release tag.
go_1_27
