{
  description = "Command-line interface for HEY email";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      supportedSystems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
      nixpkgsFor = forAllSystems (system: import nixpkgs { inherit system; });
    in {
      packages = forAllSystems (system:
        let pkgs = nixpkgsFor.${system};
        in {
          hey = pkgs.callPackage ./nix/package.nix { };
          default = self.packages.${system}.hey;
        }
      );

      devShells = forAllSystems (system:
        let
          pkgs = nixpkgsFor.${system};
          # The same toolchain the package builds with — see nix/go.nix.
          go = pkgs.callPackage ./nix/go.nix { };
        in {
          default = pkgs.mkShell {
            packages = [ go ] ++ (with pkgs; [
              actionlint
              bats
              git
              golangci-lint
              goreleaser
              gnumake
              jq
              ripgrep
              zizmor
            ]);
          };
        }
      );
    };
}
