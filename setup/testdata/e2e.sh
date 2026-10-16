#!/usr/bin/env bash
# End-to-end test of the setup wizard (fresh install and Misskey migration)
# against real PostgreSQL and Redis. Needs: go, psql, python3, openssl, curl.
#
#   PGURL=postgres://user:pass@127.0.0.1:5432/postgres?sslmode=disable \
#   PGUSER_PASS=pass setup/testdata/e2e.sh
#
# It creates (and drops) the databases e2e_fresh, e2e_mk_src and e2e_mk_dst.
set -euo pipefail
cd "$(dirname "$0")/../.."
: "${PGURL:?set PGURL to an admin connection URL}"
PGHOST_=${PGHOST_:-127.0.0.1}; PGPORT_=${PGPORT_:-5432}; PGU=${PGU:-pktest}; PGP=${PGP:-pktest}
REDIS=${REDIS:-127.0.0.1:6379}
BIN=$(mktemp -d)/community
go build -o "$BIN" ./cmd/community
WORK=$(mktemp -d)
fail() { echo "FAIL: $*" >&2; exit 1; }
db() { psql "$PGURL" -qc "DROP DATABASE IF EXISTS $1" -c "CREATE DATABASE $1"; }
dburl() { echo "postgres://$PGU:$PGP@$PGHOST_:$PGPORT_/$1?sslmode=disable"; }
start() { # dir port
  (ENV_FILE="$1/.env" LISTEN=":$2" setsid "$BIN" >>"$1/server.log" 2>&1 &)
  for _ in $(seq 50); do curl -s -o /dev/null "localhost:$2/live" && return; sleep 0.2; done; fail "server did not start"
}
token() { grep -A4 "install token" "$1/server.log" | tail -n +3 | head -1 | tr -d ' '; }
waitup() { for _ in $(seq 120); do [ "$(curl -s -o /dev/null -w '%{http_code}' "localhost:$1/api/v1/instance")" = 200 ] && return; sleep 1; done; fail "normal mode did not come up"; }
stop() { pkill -f "$BIN" || true; sleep 1; }
trap stop EXIT

echo "== fresh install"
D=$WORK/fresh; mkdir -p "$D"; db e2e_fresh; start "$D" 18190; T=$(token "$D")
[ "$(curl -s -o /dev/null -w '%{http_code}' -XPOST localhost:18190/api/setup/verify -H 'X-Setup-Token: nope')" = 401 ] || fail "bad token accepted"
curl -sf -XPOST localhost:18190/api/setup/install -H "X-Setup-Token: $T" -d "{\"locale\":\"fr\",\"mode\":\"fresh\",\"domain\":\"http://localhost:18190\",\"database\":{\"type\":\"postgres\",\"host\":\"$PGHOST_\",\"port\":$PGPORT_,\"name\":\"e2e_fresh\",\"user\":\"$PGU\",\"password\":\"$PGP\"},\"redis\":{\"address\":\"$REDIS\"},\"admin\":{\"username\":\"admin\",\"email\":\"admin@example.org\",\"password\":\"Correct-Horse-9\"}}" >/dev/null
waitup 18190
[ "$(stat -c %a "$D/.env")" = 600 ] || fail ".env mode"
grep -q '^ENCRYPTED_KEY=' "$D/.env" || fail "secret not generated"
[ -f "$D/.installed" ] || fail "marker"
[ "$(curl -s -o /dev/null -w '%{http_code}' localhost:18190/setup)" = 404 ] || fail "/setup must 404"
[ "$(curl -s -o /dev/null -w '%{http_code}' localhost:18190/api/setup/status)" = 404 ] || fail "/api/setup must 404"
curl -sf -XPOST localhost:18190/api/v1/accounts/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"Correct-Horse-9"}' | grep -q token || fail "admin login"
[ "$(psql "$(dburl e2e_fresh)" -Atc "select count(*) from system_settings where key='system.installed_at'")" = 1 ] || fail "db marker"
echo "== DB down -> maintenance, never setup"
stop; sed -i 's/^DB_PORT=.*/DB_PORT=1/' "$D/.env"; start "$D" 18190
[ "$(curl -s -o /dev/null -w '%{http_code}' localhost:18190/setup)" = 503 ] || fail "maintenance expected"
curl -s localhost:18190/api/setup/status | grep -q database_unavailable || fail "maintenance reason"
stop; mv "$D/.env" "$D/.env.bak"; start "$D" 18190
curl -s localhost:18190/api/setup/status | grep -q configuration_missing || fail "installed without .env must be maintenance"
stop

echo "== Misskey migration"
db e2e_mk_src; db e2e_mk_dst
psql "$(dburl e2e_mk_src)" -q -f setup/testdata/misskey_fixture.sql
# The fixture's source origin is http://localhost:18082.
D=$WORK/mk; mkdir -p "$D"; start "$D" 18082; T=$(token "$D")
curl -s -XPOST localhost:18082/api/setup/test/database -H "X-Setup-Token: $T" -d "{\"type\":\"postgres\",\"host\":\"$PGHOST_\",\"name\":\"e2e_fresh\",\"user\":\"$PGU\",\"password\":\"$PGP\"}" | grep -q database_not_empty || fail "installed target must be refused"
curl -s -XPOST localhost:18082/api/setup/test/misskey -H "X-Setup-Token: $T" -d "{\"host\":\"$PGHOST_\",\"name\":\"e2e_mk_src\",\"user\":\"$PGU\",\"password\":\"$PGP\"}" | grep -q '"detected_base_url":"http://localhost:18082"' || fail "domain detection"
curl -sf -XPOST localhost:18082/api/setup/install -H "X-Setup-Token: $T" -d "{\"locale\":\"ja\",\"mode\":\"misskey\",\"domain\":\"\",\"database\":{\"type\":\"postgres\",\"host\":\"$PGHOST_\",\"name\":\"e2e_mk_dst\",\"user\":\"$PGU\",\"password\":\"$PGP\"},\"redis\":{\"address\":\"$REDIS\"},\"admin\":{\"username\":\"siteadmin\",\"email\":\"ops@example.org\",\"password\":\"Correct-Horse-9\"},\"misskey\":{\"host\":\"$PGHOST_\",\"name\":\"e2e_mk_src\",\"user\":\"$PGU\",\"password\":\"$PGP\",\"media_source\":\"local\",\"files_path\":\"$PWD/setup/testdata/files\"}}" >/dev/null
waitup 18082
A=9xk2a1b2c3d4e5f6
curl -sf -XPOST localhost:18082/api/v1/accounts/login -H 'Content-Type: application/json' -d '{"username":"alice","password":"Misskey-Pass-1"}' | grep -q token || fail "Misskey password must keep working"
ACTOR=$(curl -sf -H 'Accept: application/activity+json' "localhost:18082/users/$A")
python3 - "$ACTOR" <<'PY' || fail "actor JSON-LD / key"
import json, sys, subprocess
a = json.loads(sys.argv[1])
assert a["id"] == "http://localhost:18082/users/9xk2a1b2c3d4e5f6", a["id"]
assert a["type"] == "Person" and a["preferredUsername"] == "alice"
assert "https://www.w3.org/ns/activitystreams" in json.dumps(a["@context"])
assert a["publicKey"]["id"] == a["id"] + "#main-key"
sql = open("setup/testdata/misskey_fixture.sql").read()
src = sql.split("INSERT INTO user_keypair")[1].split("'")[3].replace("''", "'")
norm = lambda pem: subprocess.run(["openssl", "pkey", "-pubin"], input=pem.encode(), capture_output=True, check=True).stdout
assert norm(a["publicKey"]["publicKeyPem"]) == norm(src), "RSA public key changed"
PY
NOTE=$(curl -sf -H 'Accept: application/activity+json' localhost:18082/notes/9xk2b1b2c3d4e5f1)
python3 - "$NOTE" <<'PY' || fail "note JSON-LD"
import json, sys
n = json.loads(sys.argv[1])
assert n["id"] == "http://localhost:18082/notes/9xk2b1b2c3d4e5f1" and n["type"] == "Note"
assert n["attributedTo"] == "http://localhost:18082/users/9xk2a1b2c3d4e5f6"
assert "@context" in n
PY
[ "$(curl -s -o /dev/null -w '%{http_code}' -H 'Accept: application/activity+json' localhost:18082/notes/9xk2b1b2c3d4e5f3)" != 200 ] || fail "followers-only note leaked"
FOLLOWS=$(psql "$(dburl e2e_mk_dst)" -Atc "select a.username||'@'||coalesce(a.domain,'') from follows f join accounts a on a.id=f.account_id join accounts b on b.id=f.follow_id where b.username='alice' and b.domain is null order by 1")
echo "$FOLLOWS" | grep -q '^carol@remote.example$' || fail "remote follower lost"
psql "$(dburl e2e_mk_dst)" -Atc "select count(*) from accounts where domain='remote.example' and username='carol'" | grep -q '^1$' || fail "remote account"
[ "$(curl -s -o /dev/null -w '%{http_code}' localhost:18082/setup)" = 404 ] || fail "/setup must 404 after migration"
echo "ALL E2E CHECKS PASSED"
