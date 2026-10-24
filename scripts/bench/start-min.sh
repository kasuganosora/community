#!/bin/sh
redis-server --save "" --appendonly no --daemonize yes --maxmemory-policy noeviction
cd /app && exec ./community
