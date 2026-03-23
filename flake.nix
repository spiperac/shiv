{
  description = "Shiv - HTTP/HTTPS interception proxy";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      
      # Method 1: Use pkgsCross directly
      mingw = nixpkgs.legacyPackages.${system}.pkgsCross.mingwW64.buildPackages;
      
      # Or Method 2: If you need multiple cross compilation targets
      # mingw = (import nixpkgs {
      #   system = "x86_64-linux";
      #   crossSystem = {
      #     config = "x86_64-w64-mingw32";
      #     libc = "msvcrt";
      #   };
      # }).buildPackages;
      
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
        ] ++ [ mingw.gcc mingw.binutils ];

        shellHook = ''
          export PATH=$PATH:$HOME/go/bin
          if [ ! -f "$HOME/go/bin/fyne" ]; then
            go install fyne.io/tools/cmd/fyne@latest
          fi
          export CC=x86_64-w64-mingw32-gcc
          export CXX=x86_64-w64-mingw32-g++
        '';
      };
    };
}
