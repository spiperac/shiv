.PHONY: test-cert test build clean install package appimage

test-cert:
	go test -v -race ./internal/cert/...
test:
	go test -v -race ./...
build:
	go build -o shiv .
clean:
	rm -f shiv
	rm -rf AppDir linuxdeploy *.AppImage
install: build
	install -Dm755 shiv $(HOME)/.local/bin/shiv
	install -Dm644 assets/logo.png $(HOME)/.local/share/icons/hicolor/256x256/apps/shiv.png
	@mkdir -p $(HOME)/.local/share/applications
	@echo "[Desktop Entry]" > $(HOME)/.local/share/applications/shiv.desktop
	@echo "Name=Shiv" >> $(HOME)/.local/share/applications/shiv.desktop
	@echo "Comment=HTTP/HTTPS Interception Proxy" >> $(HOME)/.local/share/applications/shiv.desktop
	@echo "Exec=$(HOME)/.local/bin/shiv" >> $(HOME)/.local/share/applications/shiv.desktop
	@echo "Icon=shiv" >> $(HOME)/.local/share/applications/shiv.desktop
	@echo "Type=Application" >> $(HOME)/.local/share/applications/shiv.desktop
	@echo "Categories=Development;Network;Security;" >> $(HOME)/.local/share/applications/shiv.desktop
	@echo "Terminal=false" >> $(HOME)/.local/share/applications/shiv.desktop
	@update-desktop-database $(HOME)/.local/share/applications 2>/dev/null || true
package:
	fyne package
appimage: build
	test -f linuxdeploy || (wget -qO linuxdeploy https://github.com/linuxdeploy/linuxdeploy/releases/download/continuous/linuxdeploy-x86_64.AppImage && chmod +x linuxdeploy)
	rm -rf AppDir
	mkdir -p AppDir/usr/bin AppDir/usr/lib AppDir/usr/share/applications AppDir/usr/share/icons/hicolor/256x256/apps
	cp shiv AppDir/usr/bin/
	cp assets/logo.png AppDir/usr/share/icons/hicolor/256x256/apps/shiv.png
	printf '[Desktop Entry]\nName=Shiv\nExec=shiv\nIcon=shiv\nType=Application\nCategories=Utility;\n' > AppDir/usr/share/applications/shiv.desktop
	ldd shiv | grep "=> /" | awk '{print $$3}' | xargs -I{} cp -L {} AppDir/usr/lib/
	chmod +w AppDir/usr/lib/*.so*
	ARCH=x86_64 fhs-run ./linuxdeploy --appdir AppDir --output appimage
	rm -rf AppDir
