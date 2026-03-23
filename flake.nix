{
  description = "Shiv - HTTP/HTTPS interception proxy";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      
    in {
      devShells.${system}.default = pkgs.mkShell {
        buildInputs = with pkgs; [
          go
          gcc
          libxxf86vm
          pkg-config
          xorg.libX11
          xorg.libXcursor
          xorg.libXrandr
          xorg.libXinerama
          xorg.libXi
          xorg.libXxf86vm
          libGL
          libGLU
          glibc
          glibc.dev
        ]; 

        shellHook = ''
          export PATH=$PATH:$HOME/go/bin
          if [ ! -f "$HOME/go/bin/fyne" ]; then
            go install fyne.io/tools/cmd/fyne@latest
          fi
        '';
      };
    };
}
