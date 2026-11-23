#!/bin/sh
# Runs in disposable, network-disabled containers with a test-only Docker volume.
# Optionally pass a Linux Artex-Moses-mode binary built for the Docker host architecture.
set -eu
base=postgres:16-alpine
project=$(cd "$(dirname "$0")/.." && pwd)
volume=$(docker volume create --label artex-moses-mode-purpose=permissions-regression)
trap 'docker volume rm "$volume" >/dev/null' EXIT
docker run --rm --network none --read-only --cap-drop ALL \
  --cap-add CHOWN --cap-add FOWNER --cap-add FSETID --cap-add DAC_OVERRIDE --security-opt no-new-privileges \
  -v "$volume:/app" -v "$project/scripts/prepare-volumes.sh:/test-prepare.sh:ro" \
  --entrypoint sh "$base" -ec '
    mkdir -p /app/data/old /app/keys /app/skills/custom
    echo fixture >/app/keys/test.key
    echo fixture >/app/skills/custom/note
    echo fixture >/app/program
    chmod 0444 /app/program
    chown 12345:12345 /app/skills/custom/note
    /bin/sh /test-prepare.sh
    /bin/sh /test-prepare.sh
    test "$(stat -c %u:%g /app/data)" = 10001:10001
    test "$(stat -c %a /app/keys)" = 700
    test "$(stat -c %u /app/skills/custom/note)" = 12345
  '
docker run --rm --network none --read-only --cap-drop ALL --user 10001:10001 \
  -v "$volume:/app" --entrypoint sh "$base" -ec '
    test "$(id -u)" = 10001
    test -r /app/keys/test.key
    touch /app/data/old/owner-write
    echo reviewed >>/app/skills/custom/note
    test ! -w /app/program
    echo "{\"database\":{\"host\":\"127.0.0.1\"}}" >/app/data/config.json
  '
docker run --rm --network none --read-only --cap-drop ALL --user 10002:10002 \
  -v "$volume:/app:ro" --entrypoint sh "$base" -ec 'test ! -r /app/keys/test.key && test ! -r /app/data/config.json'
if [ "$#" -gt 0 ]; then
  docker run --rm --network none --read-only --cap-drop ALL --user 10001:10001 \
    -v "$volume:/app:ro" -v "$1:/test-artex-moses-mode:ro" -e ARTEX_MOSES_MODE_CONFIG=/app/data/config.json \
    --entrypoint /test-artex-moses-mode "$base" -check-config
fi
echo "Private volumes, legacy ownership and unprivileged access verified."
