{ lib, buildGoModule, callPackage, installShellFiles, stdenv }:

let
  # The toolchain lives in nix/go.nix, shared with the flake's dev shell so
  # both build with the same Go. Bound here rather than taken as a function
  # argument: callPackage fills any argument whose name exists in pkgs, and
  # `pkgs.go` does, so a `go ? ...` default would be silently overridden by
  # the stock toolchain.
  go = callPackage ./go.nix { };
in
buildGoModule.override { inherit go; } (finalAttrs: {
  pname = "hey";
  # Bumped by `make update-nix-hash VERSION=vX.Y.Z` before each stable
  # release; scripts/release.sh refuses to tag until it matches.
  version = "0.1.1";

  src = lib.cleanSource ./..;

  # To update: run `make update-nix-hash` (Docker). It rewrites this quoted
  # value in place, so keep it a string literal rather than lib.fakeHash.
  vendorHash = "sha256-8ScxSfMZWujQThH7rVYeaH6GSOkODHqIouGJXVtlqzw=";

  subPackages = [ "cmd/hey" ];

  ldflags = [
    "-s" "-w"
    "-X github.com/basecamp/hey-cli/internal/version.Version=${finalAttrs.version}"
  ];

  nativeBuildInputs = [ installShellFiles ];

  postInstall = lib.optionalString
    (stdenv.buildPlatform.canExecute stdenv.hostPlatform) ''
    installShellCompletion --cmd hey \
      --bash <($out/bin/hey completion bash) \
      --fish <($out/bin/hey completion fish) \
      --zsh  <($out/bin/hey completion zsh)
  '';

  meta = {
    description = "Command-line interface for HEY email";
    homepage = "https://github.com/basecamp/hey-cli";
    changelog = "https://github.com/basecamp/hey-cli/releases/tag/v${finalAttrs.version}";
    license = lib.licenses.mit;
    mainProgram = "hey";
  };
})
