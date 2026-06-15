{
  description = "Shiv - HTTP/HTTPS interception proxy";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      fhs = pkgs.buildFHSEnv {
        name = "fhs-run";
        targetPkgs = p: with p; [ glibc ];
        runScript = "";
      };
    in {
      devShells.${system}.default = pkgs.mkShell {
        buildInputs = with pkgs; [
          go
          gcc
          libxxf86vm
          pkg-config
          libx11
          libxcursor
          libxrandr
          libxinerama
          libxi
          libxxf86vm
          wayland
          wayland-protocols
          libxkbcommon
          libGL
          libGLU
          glibc
          glibc.dev
          fhs
          fuse
        ];
        shellHook = ''
          export PATH=$PATH:$HOME/go/bin
          export APPIMAGE_EXTRACT_AND_RUN=1
          if [ ! -f "$HOME/go/bin/fyne" ]; then
            go install fyne.io/tools/cmd/fyne@latest
          fi
        '';
      };
    };
}
