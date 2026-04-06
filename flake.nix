{
  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-parts.url = "github:hercules-ci/flake-parts";
    git-hooks.url = "github:cachix/git-hooks.nix";
  };

  outputs =
    inputs@{
      flake-parts,
      nixpkgs,
      git-hooks,
      ...
    }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      imports = [
        git-hooks.flakeModule
      ];

      perSystem =
        {
          pkgs,
          config,
          system,
          ...
        }:
        {
          devShells.default = pkgs.mkShell {
            packages = config.pre-commit.settings.enabledPackages;
            buildInputs = with pkgs; [
              # Git & Version Control
              git
              just
              convco
              prek

              # Go
              go
              gcc

              gnumake
            ];

            shellHook = ''
              ${config.pre-commit.installationScript}
            '';
          };

          pre-commit = {
            check.enable = false;

            settings = {
              package = pkgs.prek;
              hooks = {
                # Security checks
                trufflehog.enable = true;
                detect-private-keys.enable = true;
                check-added-large-files.enable = true;

                # Code formatting
                prettier = {
                  excludes = [ "\\.web-docs/.*" "docs-partials/.*" ];
                  enable = true;
                };

                # File integrity
                check-symlinks.enable = true;
                check-case-conflicts.enable = true;
                end-of-file-fixer = {
                  enable = true;
                  excludes = [ "\\.web-docs/.*" "docs-partials/.*" ];
                };
                trim-trailing-whitespace = {
                  enable = true;
                  excludes = [
                    "node_modules"
                    "\\.web-docs/.*"
                    "docs-partials/.*"
                  ];
                };
                mixed-line-endings.enable = true;
                fix-byte-order-marker.enable = true;

                # Shell scripts
                check-executables-have-shebangs.enable = true;
                check-shebang-scripts-are-executable.enable = true;

                gofmt.enable = true;
                golangci-lint.enable = true;
                gotest.enable = true;
                staticcheck.enable = true;
                actionlint.enable = true;
              };
            };
          };
        };
    };
}
