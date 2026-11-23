#!/usr/bin/env bash
# Artex-Moses-mode install script: 1) all-Docker  2) local build & run
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
ask(){  local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }
rand(){ head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24; }
# json_str prints $1 as a JSON string literal: backslash, double quote and the
# control characters read -r can return are escaped.
json_str(){
  local s="$1"
  s="${s//\\/\\\\}"; s="${s//\"/\\\"}"
  s="${s//$'\t'/\\t}"; s="${s//$'\r'/\\r}"
  printf '"%s"' "$s"
}
# sed_repl escapes $1 for the replacement side of s|...|...|: \, | and &.
sed_repl(){ printf '%s' "$1" | sed 's/[\\|&]/\\&/g'; }

# ── docker environment check / auto-install ───────────────────
ensure_docker(){
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    ok "docker and docker compose detected"; return
  fi
  warn "docker / docker compose not detected"
  case "$(uname -s)" in
    Linux)
      if [ "$(ask 'Install Docker automatically? (y/n)' y)" = y ]; then
        curl -fsSL https://get.docker.com | sh
        sudo usermod -aG docker "$USER" || true
        ok "Docker installed (re-login required for the group change to take effect)"
      else
        die "please install docker yourself and retry"
      fi ;;
    Darwin) die "on macOS please install Docker Desktop: https://www.docker.com/products/docker-desktop/" ;;
    *)      die "please install docker yourself and retry" ;;
  esac
}

# ── 1) All-Docker ───────────────────────────────
install_docker(){
  ensure_docker
  if [ ! -f .env ]; then
    umask 077
    cp .env.example .env 2>/dev/null || true
    local pw key
    pw="$(ask 'Postgres password (press Enter to generate randomly)' "$(rand)")"
    key="$(ask 'ANTHROPIC_API_KEY (may be empty, configure later in the UI)' '')"
    sed -i.bak "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=$(sed_repl "$pw")|" .env
    sed -i.bak "s|^ANTHROPIC_API_KEY=.*|ANTHROPIC_API_KEY=$(sed_repl "$key")|" .env
    rm -f .env.bak
    ok ".env generated (POSTGRES_PASSWORD set)"
  else
    info "reusing existing .env"
  fi
  info "building the image from this checkout and starting..."
  docker compose up -d --build
  ok "started → http://localhost:8787"
  info "view logs: docker compose logs -f artex-moses-mode"
}

# ── 2) Local build & run ──────────────────────────────
install_local(){
  command -v go >/dev/null 2>&1 || die "Go is required for a local build (see go.mod)."
  local current_config="${ARTEX_MOSES_MODE_CONFIG:-config.json}" install_stage start_database=0 configure_database=0
  install_stage="$(mktemp -d ./.artex-moses-mode-install.XXXXXXXX)"
  # Expand the path now since EXIT also runs after local variables leave scope.
  # shellcheck disable=SC2064
  trap "rm -rf -- '$install_stage'" EXIT
  if [ -L "$current_config" ]; then current_config="$(realpath "$current_config")"; fi
  if [ -e "$current_config" ] && [ ! -f "$current_config" ]; then die "Configuration path must reference a regular file"; fi

  # embedding the frontend requires node to produce the static assets
  if command -v npm >/dev/null 2>&1; then
    info "building frontend static assets..."
    ( cd web && npm ci && npm run build:static )
    mkdir -p server/webui
    rm -rf server/webui/dist && cp -r web/out server/webui/dist
    info "compiling embedded single binary..."
    CGO_ENABLED=0 go build -tags embedui -trimpath -o "$install_stage/artex-moses-mode" ./cmd/artex-moses-mode
  else
    warn "npm not detected: building a backend WITHOUT the embedded frontend (run npm run dev separately for the frontend)"
    CGO_ENABLED=0 go build -o "$install_stage/artex-moses-mode" ./cmd/artex-moses-mode
  fi
  if [ -f "$current_config" ]; then
    cp "$current_config" "$install_stage/original.json"
    if ARTEX_MOSES_MODE_CONFIG="$install_stage/original.json" "$install_stage/artex-moses-mode" -check-config; then
      info "reusing existing configuration"
    else
      [ "$?" -eq 3 ] || die "invalid existing configuration"
      configure_database=1
    fi
  elif [ -z "${ARTEX_MOSES_MODE_PG_DSN:-}" ]; then
    configure_database=1
  fi
  if [ "$configure_database" = 1 ]; then
    echo "Database setup:"
    echo "  1) Connect to an existing PostgreSQL"
    echo "  2) Start a PostgreSQL with Docker (requires docker)"
    case "$(ask 'Choose' 1)" in
      2)
        ensure_docker
        local pw; pw="$(ask 'Postgres password (press Enter for random)' "$(rand)")"
        start_database=1
        DB_HOST=127.0.0.1 DB_PORT=5432 DB_USER=artex_moses_mode DB_PASS="$pw" DB_NAME=artex_moses_mode DB_SSL=disable ;;
      *)
        DB_HOST="$(ask 'Database host' 127.0.0.1)"
        DB_PORT="$(ask 'Port' 5432)"
        DB_USER="$(ask 'User' artex_moses_mode)"
        DB_PASS="$(ask 'Password' '')"
        DB_NAME="$(ask 'Database name' artex_moses_mode)"
        DB_SSL="$(ask 'sslmode (disable/require)' disable)" ;;
    esac

    # generate config.json (string values JSON-escaped: a password may contain " or \)
    case "$DB_PORT" in (*[!0-9]*|'') die "port must be a number: $DB_PORT" ;; esac
    cat > "$install_stage/config.json" <<JSON
{
  "database": {
    "host": $(json_str "$DB_HOST"),
    "port": ${DB_PORT},
    "user": $(json_str "$DB_USER"),
    "password": $(json_str "$DB_PASS"),
    "dbname": $(json_str "$DB_NAME"),
    "sslmode": $(json_str "$DB_SSL")
  }
}
JSON
    if [ -f "$install_stage/original.json" ]; then
      ARTEX_MOSES_MODE_CONFIG="$install_stage/original.json" "$install_stage/artex-moses-mode" -merge-install-db < "$install_stage/config.json" > "$install_stage/merged.json"
      mv "$install_stage/merged.json" "$install_stage/config.json"
    fi
    ARTEX_MOSES_MODE_CONFIG="$install_stage/config.json" "$install_stage/artex-moses-mode" -check-config
    (
      # Stage on the destination filesystem and serialize competing installers.
      lock="$current_config.install-lock"
      mkdir -m 700 "$lock" || die "another installer owns the configuration lock at $lock"
      trap 'rm -f -- "$lock/config"; rmdir "$lock"' EXIT
      if [ -f "$install_stage/original.json" ]; then
        cmp -s "$install_stage/original.json" "$current_config" || die "configuration changed during install; retry"
      elif [ -e "$current_config" ] || [ -L "$current_config" ]; then
        die "a configuration appeared during install; retry"
      fi
      (umask 077; cat "$install_stage/config.json" > "$lock/config")
      if [ "$start_database" = 1 ]; then
        docker run -d --name artex-moses-mode-pg -p 127.0.0.1:5432:5432 \
          -e POSTGRES_USER=artex_moses_mode -e POSTGRES_PASSWORD="$DB_PASS" -e POSTGRES_DB=artex_moses_mode \
          -v artex-moses-mode-pg:/var/lib/postgresql/data postgres:16-alpine
      fi
      if [ -f "$install_stage/original.json" ]; then
        cmp -s "$install_stage/original.json" "$current_config" || die "configuration changed during install; retry"
        mv -f -- "$lock/config" "$current_config"
      else
        ln "$lock/config" "$current_config"
      fi
    )
  fi
  if [ -f "$current_config" ]; then chmod go-rwx "$current_config"; fi
  mv -f -- "$install_stage/artex-moses-mode" ./artex-moses-mode
  ok "build complete → ./artex-moses-mode"

  info "starting... (Ctrl-C to exit)"
  ./artex-moses-mode
}

echo "=============================="
echo "  Artex-Moses-mode Install"
echo "  1) All-Docker install"
echo "  2) Run locally (go build)"
echo "=============================="
case "$(ask 'Choose' 1)" in
  1) install_docker ;;
  2) install_local ;;
  *) die "invalid choice" ;;
esac
