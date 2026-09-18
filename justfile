# switchboard — coordination service for parallel sessions

set shell := ["bash", "-euo", "pipefail", "-c"]

bin       := "switchboard"
prefix    := env_var_or_default("PREFIX", env_var("HOME") + "/.local")
bindir    := prefix + "/bin"
unit      := env_var("HOME") + "/.config/systemd/user/switchboard.service"
confdir   := env_var("HOME") + "/.config/switchboard"
conffile  := confdir + "/switchboard.env"
db        := env_var_or_default("XDG_DATA_HOME", env_var("HOME") + "/.local/share") + "/switchboard/switchboard.db"
devdb     := "/tmp/switchboard-dev.db"
# A build must always be identifiable. Before the first commit git describe has
# nothing to say, so fall back to a timestamp rather than to a constant that
# would make two deploys look the same.
version   := `git describe --tags --always --dirty 2>/dev/null || date -u +dev-%Y%m%d-%H%M%S`
commit    := `git rev-parse --short HEAD 2>/dev/null || echo unknown`
date      := `date -u +%Y-%m-%dT%H:%M:%SZ`
ldflags   := "-X switchboard/internal/build.Version=" + version + " -X switchboard/internal/build.Commit=" + commit + " -X switchboard/internal/build.Date=" + date

# List the recipes
default:
    @just --list

# Build the binary, stamped with the current version
build:
    go build -ldflags "{{ldflags}}" -o {{bin}} ./cmd/switchboard
    @echo "built {{version}}"

# Run the test suite
test:
    go test ./...

# Tests with the race detector
race:
    go test -race ./...

# Point git at .githooks, so the guards in it actually run
hooks:
    git config core.hooksPath .githooks
    @echo "hooks enabled: $(git config core.hooksPath)"

# Formatting and static analysis
lint:
    gofmt -l ./cmd ./internal
    go vet ./...

# Run the service in the foreground, on the installed database
run *ARGS:
    go run -ldflags "{{ldflags}}" ./cmd/switchboard serve {{ARGS}}

# Run a development service: another port, another database, short delays
dev:
    @if [ "{{devdb}}" = "{{db}}" ]; then echo "refusing: dev would use the installed database"; exit 1; fi
    go run -ldflags "{{ldflags}}" ./cmd/switchboard serve \
        --addr 127.0.0.1:8799 --db {{devdb}} \
        --debounce 5s --min-interval 10s --urgent 2s --undo-window 10s

# --- installation ----------------------------------------------------------

# Install the binary, the unit and an example config, then start the service
install: build
    install -Dm755 {{bin}} {{bindir}}/{{bin}}
    install -Dm644 systemd/switchboard.service {{unit}}
    @mkdir -p {{confdir}}
    @if [ ! -f {{conffile}} ]; then \
        install -m644 packaging/switchboard.env.example {{conffile}}; \
        echo "wrote {{conffile}}"; \
    else \
        echo "kept {{conffile}} as it is"; \
    fi
    systemctl --user daemon-reload
    systemctl --user enable --now switchboard.service
    @just _await-health
    @echo
    @{{bindir}}/{{bin}} db status

# Remove the service, the unit and the binary. The database and its backups stay.
uninstall:
    -systemctl --user disable --now switchboard.service
    rm -f {{unit}}
    systemctl --user daemon-reload
    rm -f {{bindir}}/{{bin}} {{bindir}}/{{bin}}.previous
    @echo "removed. The database and its backups are still in $(dirname {{db}})"

# --- deploying an update ---------------------------------------------------

# Check, build, back up, swap, restart, verify — and put the old one back if it fails
deploy: lint test build
    @echo "--- backing up"
    @if systemctl --user is-active --quiet switchboard.service; then \
        {{bindir}}/{{bin}} db backup || true; \
    else \
        echo "service not running, nothing to back up"; \
    fi
    @echo "--- installing {{version}}"
    @if [ -f {{bindir}}/{{bin}} ]; then cp -f {{bindir}}/{{bin}} {{bindir}}/{{bin}}.previous; fi
    install -Dm755 {{bin}} {{bindir}}/{{bin}}
    install -Dm644 systemd/switchboard.service {{unit}}
    systemctl --user daemon-reload
    systemctl --user restart switchboard.service
    @echo "--- verifying"
    @if just _verify "{{version}}"; then \
        echo "deployed {{version}}"; \
    else \
        echo "verification failed — rolling back"; \
        just rollback; \
        exit 1; \
    fi

# Put the previous binary back and restart
rollback:
    @if [ ! -f {{bindir}}/{{bin}}.previous ]; then echo "no previous binary kept"; exit 1; fi
    mv -f {{bindir}}/{{bin}}.previous {{bindir}}/{{bin}}
    systemctl --user restart switchboard.service
    @just _await-health
    @{{bindir}}/{{bin}} version

# --- looking after it ------------------------------------------------------

# What the service says about itself
health:
    @curl -fsS http://127.0.0.1:8787/v1/health | python3 -m json.tool

# Where the database is, what it holds
status:
    @{{bindir}}/{{bin}} db status

# A copy of the database, taken while the service runs
backup:
    @{{bindir}}/{{bin}} db backup

# Follow the service log
logs:
    journalctl --user -u switchboard.service -f

# --- internals -------------------------------------------------------------

# Wait for the service to answer, up to ten seconds
_await-health:
    @addr=$(grep -E '^SWITCHBOARD_ADDR=' {{conffile}} 2>/dev/null | cut -d= -f2 || true); \
    addr=${addr:-127.0.0.1:8787}; \
    for i in $(seq 1 20); do \
        if curl -fsS "http://$addr/v1/health" > /dev/null 2>&1; then exit 0; fi; \
        sleep 0.5; \
    done; \
    echo "the service did not answer on $addr"; \
    systemctl --user --no-pager status switchboard.service | tail -15; \
    exit 1

# The service answers, runs the expected build, and its schema is up to date
_verify WANT:
    @addr=$(grep -E '^SWITCHBOARD_ADDR=' {{conffile}} 2>/dev/null | cut -d= -f2 || true); \
    addr=${addr:-127.0.0.1:8787}; \
    for i in $(seq 1 20); do \
        body=$(curl -fsS "http://$addr/v1/health" 2>/dev/null) && break; \
        sleep 0.5; \
    done; \
    if [ -z "${body:-}" ]; then echo "no answer from $addr"; exit 1; fi; \
    got=$(echo "$body" | python3 -c 'import json,sys; print(json.load(sys.stdin)["version"])'); \
    ok=$(echo "$body"  | python3 -c 'import json,sys; print(json.load(sys.stdin)["ok"])'); \
    if [ "$got" != "{{WANT}}" ]; then echo "running $got, expected {{WANT}}"; exit 1; fi; \
    if [ "$ok" != "True" ]; then echo "schema is not up to date: $body"; exit 1; fi; \
    echo "$body"
