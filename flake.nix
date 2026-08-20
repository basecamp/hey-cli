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
        let pkgs = nixpkgsFor.${system};
        in {
          default = pkgs.mkShell {
            packages = with pkgs; [
              actionlint
              bats
              git
              go_1_26
              golangci-lint
              goreleaser
              gnumake
              jq
              ripgrep
              zizmor
            ];
          };
        }
      );
    };
}
