# Databasify MariaDB Engine

- [Introduction](#introduction)
- [Lines](#lines)
- [Features](#features)
- [Ports](#ports)
- [Running Locally](#running-locally)
- [Adding a Version Line](#adding-a-version-line)

## Introduction

The MariaDB engine for [Databasify](https://github.com/databasifylabs/databasify), built on the [engine SDK](https://github.com/databasifylabs/engine-sdk).

Each image is the official `mariadb` image plus its tools and `databasify`, the [engine SDK](https://github.com/databasifylabs/engine-sdk) 1.0 runtime that runs as PID 1. Images are published as `ghcr.io/databasifylabs/databasify-mariadb`.

Engine behaviour lives in the Databasify app: its PHP driver for this engine runs Bash scripts inside the container through the runtime. This repository changes only when the database or one of its tools moves to a new version.

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
| Host tuning | ✓ |

## Ports

| Port | Use |
| --- | --- |
| 25060 | `client`, for applications |
| 4444, 4567, 4568 | `peers`: SST, Galera and IST |

## Running Locally

`$IMAGES` is `tools/images.sh` in a clone of [engine-sdk](https://github.com/databasifylabs/engine-sdk) at the tag the `sdk` pin in `versions.json` names (`v1.0.0`). Run it from this repository's root:

```bash
$IMAGES check
$IMAGES build mariadb-12.3 --load --tag smoke:mariadb-12.3
$IMAGES smoke smoke:mariadb-12.3 mariadb
```

The smoke test starts the image, runs `images/mariadb/smoke/check.sh` and then the SDK's runtime conformance suite.

## Adding a Version Line

Follow `.claude/skills/adding-a-version-line/SKILL.md`. It covers the `versions.json` entry, what differs per line, and the checks to run.
