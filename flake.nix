{
  description = "fbs-core — S3-compatible object storage server";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
        go = pkgs.go_1_27;

        fbs-core = pkgs.buildGoModule {
          inherit go;
          pname = "fbs-core";
          version = "0.2.0";
          src = ./.;
          vendorHash = "sha256-l7k8U8l4WyHIaHUIJIKjM5OZvzwGy+MfUXPWbrCOHDw=";
          subPackages = [ "./cmd/server" ];
          tags = [ "osusergo" "netgo" ];
          ldflags = [ "-s" "-w" ];
        };
      in
      {
        packages.default = fbs-core;

        devShells.default = pkgs.mkShell {
          packages = [
            go
            pkgs.gopls
            pkgs.gotools
            pkgs.golangci-lint
          ];

          shellHook = ''
            echo "fbs-core dev shell"
            echo "  go $(go version | sed 's/go version //')"
          '';
        };
      }
    );
}
