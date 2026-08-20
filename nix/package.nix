{ lib, buildGoModule, fetchurl, go_1_26, installShellFiles, stdenv }:

let
  # go.mod's `go` directive is the floor; nixpkgs-unstable lagged it at 1.26.5
  # when this flake was written, so the toolchain is rebuilt from the upstream
  # source tarball until nixpkgs catches up. The override is conditional and
  # drops itself once go_1_26 reaches 1.26.6 — delete this block after a
  # `nix flake update` that makes `lib.versionOlder` false. Keep MIN_GO in
  # step with go.mod: a toolchain bump that outpaces flake.lock breaks the
  # build without touching go.mod or go.sum, which is why the nix-build CI job
  # runs on every go.mod change and on tags.
  minGo = "1.26.6";
  go = if lib.versionOlder go_1_26.version minGo
    then go_1_26.overrideAttrs (_: {
      version = minGo;
      src = fetchurl {
        url = "https://go.dev/dl/go${minGo}.src.tar.gz";
        hash = "sha256-oHIcVMaIkBRI13rZs+x+p8R0cwdV/4kTgukuy5P/LLE=";
      };
    })
    else go_1_26;
in
buildGoModule.override { inherit go; } (finalAttrs: {
  pname = "hey";
  # Updated automatically by scripts/update-nix-flake.sh on each release.
  version = "0.1.1";

  src = lib.cleanSource ./..;

  # To update: run `make update-nix-hash` (Docker). It rewrites this quoted
  # value in place, so keep it a string literal rather than lib.fakeHash.
  vendorHash = "sha256-Yf+Y7DFm6rBFxX7evMfGY7/O4l7bk5LP1++ZJmW7O4I=";

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
