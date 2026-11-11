# Databasify MariaDB Engine

- [Introduction](#introduction)
- [Lines](#lines)
- [Features](#features)
- [Ports](#ports)
- [Engine Notes](#engine-notes)
- [Running Locally](#running-locally)
- [Adding a Version Line](#adding-a-version-line)

## Introduction

The MariaDB engine for [Databasify](https://github.com/databasifylabs/databasify), built on the [engine SDK](https://github.com/databasifylabs/engine-sdk).

Each image is the official `mariadb` image plus `databasify`, the engine binary that runs as PID 1 and answers the SDK's typed commands. Images are published as `ghcr.io/databasifylabs/databasify-mariadb`.

The commands and the flows Databasify runs are the same for every engine; see the SDK's [Flows](https://github.com/databasifylabs/engine-sdk#flows).

## Lines

MariaDB 10.11, 11.4, 11.8 and 12.3. Each line takes patches of its release only and never leaves it. The weekly refresh moves the pins in `versions.json`.

## Features

| Feature | MariaDB |
| --- | --- |
| Node lifecycle, status, metrics, TLS | ✓ |
| Databases and users with read/write grants | ✓ |
| Password encryptions | `mysql_native_password`, `ed25519`; 11.8 and 12.3 also `parsec` |
| High availability | Galera |
| Automatic and manual failover, force quorum | ✓ |
| Standbys (up to 2) and read-only nodes (any number) | ✓ |
| Primary key required in a cluster | ✓ |
| Connection pools | — |
| Backups and point-in-time restore | mariadb-backup and binary logs |
| Continuous migration | mariadb-dump and GTID replication |
| Fork (one-shot copy) | ✓ |
| Query insights | statement digests |
| Log forwarding and host tuning | ✓ |

A command the engine doesn't offer answers `unsupported`; `engine capabilities` lists the ones it does.

## Ports

| Port | Use |
| --- | --- |
| 25060 | `client`, for applications |
| 4444, 4567, 4568 | `peers`: SST, Galera and IST |

## Engine Notes

- The engine builds on [engine-mysql-family](https://github.com/databasifylabs/engine-mysql-family).
- MariaDB doesn't record statements run as server-side prepared statements; `query insights` says so in `reason`.

## Running Locally

`$IMAGES` is `tools/images.sh` in a clone of [engine-sdk](https://github.com/databasifylabs/engine-sdk) at the version `go.mod` requires. Run it from this repository's root:

```bash
go vet ./... && go test -race -count=1 ./...
$IMAGES build mariadb-12.3 --load --tag smoke:mariadb-12.3
$IMAGES smoke smoke:mariadb-12.3 mariadb
```

The smoke test starts the image, runs `images/mariadb/smoke/check.sh` and then the SDK's conformance suite.

## Adding a Version Line

Follow `.claude/skills/adding-a-version-line/SKILL.md`. It covers the `versions.json` entry, the code that differs per line in this engine, and the checks to run.
