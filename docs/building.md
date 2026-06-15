# Building from Source

## Requirements

- Go 1.22 or later
- A C compiler (gcc or clang) — required by Fyne for CGo
- Platform graphics libraries:
    - **Linux**: `libgl1-mesa-dev` and `xorg-dev`
    - **macOS**: Xcode command-line tools (`xcode-select --install`)
    - **Windows**: MinGW (`gcc-mingw-w64-x86-64` from MSYS2 or a MinGW package)

## Quick build

```
go build -o shiv .
./shiv
```

## Using Nix

The repository includes a Nix flake that provides a reproducible development shell with all native dependencies set up correctly. This is the recommended way to build on Linux if you use Nix:

```
nix develop
go build -o shiv .
```

## Installing locally (Linux)

```
make install
```

This builds the binary, installs it to `~/.local/bin/shiv`, copies the icon, and registers a `.desktop` entry so Shiv appears in your application launcher.

## AppImage (Linux)

The AppImage bundles the binary with its shared library dependencies so it runs on any modern Linux distribution without installing anything.

```
make appimage
```

This produces a `Shiv-x86_64.AppImage` in the current directory. Mark it executable and run it directly:

```
chmod +x Shiv-x86_64.AppImage
./Shiv-x86_64.AppImage
```

## Packaged builds with Fyne

Fyne's packaging tool produces platform-native bundles — a `.tar.xz` on Linux, a `.app` on macOS, and an `.exe` on Windows:

```
fyne package
```

Install the Fyne CLI with:

```
go install fyne.io/tools/cmd/fyne@latest
```

## Cross-compilation

The release CI builds Windows binaries on Linux using MinGW and macOS universal binaries using two separate `fyne package` runs (one for arm64, one for amd64) combined with `lipo`. See `.github/workflows/release.yml` for the exact steps if you need to replicate this locally.

## Running tests

```
go test ./...
```

The non-UI packages (`internal/store`, `internal/events`, `internal/proxy`, `internal/http`) can be tested without a display. The UI package is not unit tested and requires a running display or Xvfb.
