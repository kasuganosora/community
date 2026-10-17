#!/bin/sh
# Minimum tier: Community + Redis + SQLite in ONE container capped at 1 CPU / 1 GB (swap disabled).
# Prereqs: static binaries built into $BENCH (CGO_ENABLED=0 go build -o $BENCH/community ./cmd/community; same for tootctl),
# frontend built (npm --prefix frontend ci && npm --prefix frontend run build). Run from the repository root.
set -e
BENCH=${BENCH:-/tmp/community-bench}; mkdir -p $BENCH/min; cp scripts/bench/start-min.sh $BENCH/; touch $BENCH/min/.env
docker rm -f cmin >/dev/null 2>&1 || true
docker run -d --name cmin --memory=1g --memory-swap=1g --cpus=1 -p 18080:8080 \
 -v $BENCH:/app -v $PWD/frontend/dist:/app/frontend/dist:ro \
 -e APP_ROOT=/app -e ENV_FILE=/app/min/.env -e DB_CONNECTION=sqlite -e DB_HOST=/app/min/community.db \
 -e ENCRYPTED_KEY=$(openssl rand -hex 16) -e APP_BASE_URL=http://localhost:18080 -e LISTEN=0.0.0.0:8080 \
 -e REDIS_0_HOST=127.0.0.1 -e REDIS_0_PORT=6379 -e STORAGE_TYPE=local -e MEILISEARCH_ENABLED=false \
 redis:7-alpine /app/start-min.sh
for i in $(seq 1 60); do curl -fs -o /dev/null localhost:18080/ready && break; sleep 2; done
for i in $(seq 1 10); do docker exec cmin sh -c "cd /app && echo benchpass$i | ./tootctl accounts create u$i --email u$i@bench.test --confirmed --approve --password-stdin" >/dev/null; done
APP=$(curl -s -X POST localhost:18080/api/v1/apps -d client_name=bench -d redirect_uris=urn:ietf:wg:oauth:2.0:oob -d scopes="read write follow")
CID=$(echo $APP|python3 -c 'import sys,json;print(json.load(sys.stdin)["client_id"])'); CS=$(echo $APP|python3 -c 'import sys,json;print(json.load(sys.stdin)["client_secret"])')
sleep 30; docker exec cmin sh -c 'echo cgroup=$(cat /sys/fs/cgroup/memory.current); ps -o rss,comm' > $BENCH/min/idle.txt
( while true; do echo "$(date +%T) $(docker exec cmin sh -c 'cat /sys/fs/cgroup/memory.current; ps -o rss,comm | grep -E "community|redis"' | tr '\n' ' ')" >> $BENCH/min/mem.txt; sleep 5; done ) & W=$!
python3 scripts/bench/load.py http://localhost:18080 $CID $CS 10 ${DUR:-600} 8 > $BENCH/min/load.txt
kill $W
docker exec cmin sh -c 'echo peak=$(cat /sys/fs/cgroup/memory.peak); grep -E "^(anon|file) " /sys/fs/cgroup/memory.stat; grep oom_kill /sys/fs/cgroup/memory.events' > $BENCH/min/final.txt
docker inspect cmin --format 'OOMKilled={{.State.OOMKilled}}' >> $BENCH/min/final.txt
docker rm -f cmin
cat $BENCH/min/idle.txt $BENCH/min/load.txt $BENCH/min/final.txt
