.PHONY: test-cert test build clean install 
test-cert:
	go test -v -race ./internal/cert/...

test:
	go test -v -race ./...

build:
	go build -o shiv .

clean:
	rm -f shiv

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
