#!/bin/sh
# Recommended tier: PostgreSQL + Redis + Meilisearch + Community, each container with hard limits (memory-swap = memory).
# Usage (from repo root, binaries built into $BENCH as in run-minimum.sh): scripts/bench/run-recommended.sh 2g|4g
# Uses --network host with non-default ports (15432, 16379, 17700, 18081).
set -e
T=$1; D=${BENCH:-/tmp/community-bench}/rec$T; mkdir -p $D ${BENCH:-/tmp/community-bench}/min; touch ${BENCH:-/tmp/community-bench}/min/.env; rm -rf $D/*
case $T in 2g) A=640m P=512m R=192m M=704m;; 4g) A=1280m P=1024m R=384m M=1408m;; esac
docker rm -f rapp rpg rredis rmeili >/dev/null 2>&1 || true
docker network create rnet >/dev/null 2>&1 || true
docker run -d --name rpg --network host --memory=$P --memory-swap=$P --cpus=0.5 -e POSTGRES_PASSWORD=benchpw -e POSTGRES_DB=community postgres:17-alpine -c port=15432 >/dev/null
docker run -d --name rredis --network host --memory=$R --memory-swap=$R --cpus=0.2 redis:7-alpine redis-server --port 16379 >/dev/null
docker run -d --name rmeili --network host --memory=$M --memory-swap=$M --cpus=0.3 -e MEILI_MASTER_KEY=benchmasterkeybenchmasterkey -e MEILI_ENV=production getmeili/meilisearch:v1.12 meilisearch --http-addr 127.0.0.1:17700 >/dev/null
sleep 12
ip(){ docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $1; }
PG=127.0.0.1; RD=127.0.0.1; ML=127.0.0.1
docker run -d --name rapp --network host --memory=$A --memory-swap=$A --cpus=1 \
 -v ${BENCH:-/tmp/community-bench}:/app -v $PWD/frontend/dist:/app/frontend/dist:ro -w /app \
 -e APP_ROOT=/app -e ENV_FILE=/app/min/.env -e DB_CONNECTION=postgres -e DB_HOST=$PG -e DB_PORT=15432 -e DB_DATABASE=community -e DB_USERNAME=postgres -e DB_PASSWORD=benchpw -e DB_SSLMODE=disable \
 -e ENCRYPTED_KEY=$(openssl rand -hex 16) -e APP_BASE_URL=http://localhost:18081 -e LISTEN=0.0.0.0:18081 \
 -e REDIS_0_HOST=$RD -e REDIS_0_PORT=16379 -e STORAGE_TYPE=local \
 -e MEILISEARCH_ENABLED=true -e MEILISEARCH_HOST=http://$ML:17700 -e MEILISEARCH_API_KEY=benchmasterkeybenchmasterkey \
 alpine:3.20 ./community >/dev/null
for i in $(seq 1 40); do curl -fs -o /dev/null localhost:18081/ready && break; sleep 2; done
curl -fs localhost:18081/ready && echo ready || { docker logs rapp 2>&1 | grep -v GIN-debug | tail; exit 1; }
for i in $(seq 1 10); do docker exec -w /app rapp sh -c "echo benchpass$i | ./tootctl accounts create u$i --email u$i@bench.test --confirmed --approve --password-stdin" >/dev/null 2>&1; done
APP=$(curl -s -X POST localhost:18081/api/v1/apps -d client_name=bench -d redirect_uris=urn:ietf:wg:oauth:2.0:oob -d scopes="read write follow")
CID=$(echo $APP|python3 -c 'import sys,json;print(json.load(sys.stdin)["client_id"])'); CS=$(echo $APP|python3 -c 'import sys,json;print(json.load(sys.stdin)["client_secret"])')
sleep 30
docker stats --no-stream --format '{{.Name}} {{.MemUsage}}' rapp rpg rredis rmeili > $D/idle.txt
( while true; do docker stats --no-stream --format '{{.Name}} {{.MemUsage}} {{.CPUPerc}}' rapp rpg rredis rmeili | sed "s/^/$(date +%T) /" >> $D/mem.txt; sleep 5; done ) & W=$!
python3 scripts/bench/load.py http://localhost:18081 $CID $CS 10 ${DUR:-600} 8 > $D/load.txt 2>&1 || true
kill $W
for c in rapp rpg rredis rmeili; do echo "$c peak=$(docker exec $c cat /sys/fs/cgroup/memory.peak 2>/dev/null) $(docker inspect $c --format 'OOMKilled={{.State.OOMKilled}} status={{.State.Status}}') $(docker exec $c grep oom_kill /sys/fs/cgroup/memory.events 2>/dev/null)"; done > $D/final.txt
docker rm -f rapp rpg rredis rmeili >/dev/null; cat $D/idle.txt $D/load.txt $D/final.txt
