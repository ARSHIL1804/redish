# Redish

A small Redis-compatible TCP server written in Go.

## Run

```bash
go run .
```

The default endpoint is `0.0.0.0:7379`.

## Configuration

```bash
go run . \
  -port 7379 \
  -max-keys 1000 \
  -eviction-policy lfu \
  -aof ./data/appendonly.aof \
  -aof-fsync-interval 1 \
  -aof-rewrite-min-size 67108864
```

Use `go run . -h` for all options.

Supported eviction policies are `lfu` (default) and `lru`. Eviction is volatile: only keys with an expiration time are eligible.

## Persistence

Redish writes mutation records to the configured append-only file. The file is replayed on startup, flushed and synced at the configured interval, and compacted in the background after it reaches the rewrite threshold.

## Test

```bash
go test ./...
```

## Layout

```text
config/       Runtime configuration defaults
core/         RESP protocol, commands, store, eviction, expiration, and AOF
main.go       Flags and epoll TCP server
```
