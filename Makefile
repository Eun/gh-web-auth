.PHONY: build install uninstall run clean

BINARY := gh-web-auth
INSTALL_DIR := /usr/local/bin
SERVICE_FILE := gh-web-auth.service
SERVICE_DIR := /etc/systemd/system

build:
	go build -o $(BINARY) .

run: build
	./$(BINARY)

install: build
	cp $(BINARY) $(INSTALL_DIR)/$(BINARY)
	cp $(SERVICE_FILE) $(SERVICE_DIR)/$(SERVICE_FILE)
	systemctl daemon-reload
	systemctl enable $(BINARY)
	systemctl start $(BINARY)

uninstall:
	systemctl stop $(BINARY) || true
	systemctl disable $(BINARY) || true
	rm -f $(SERVICE_DIR)/$(SERVICE_FILE)
	rm -f $(INSTALL_DIR)/$(BINARY)
	systemctl daemon-reload

clean:
	rm -f $(BINARY)
