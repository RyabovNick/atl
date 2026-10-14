BIN ?= $(HOME)/.local/bin
SKILLS ?= $(HOME)/.config/opencode/skills

.PHONY: build test install

build:
	go build -o atl .

test:
	go vet ./...
	go test ./...

install: test
	go build -o $(BIN)/atl .
	install -D -m 644 skill/atlassian/SKILL.md $(SKILLS)/atlassian/SKILL.md
