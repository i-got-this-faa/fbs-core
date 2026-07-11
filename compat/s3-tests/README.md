# S3 compatibility testing (ceph/s3-tests)

This directory is **tracked in git** so the whole team can run the same
AWS-style compatibility suite and review the feature checklist.

It wires [ceph/s3-tests](https://github.com/ceph/s3-tests) (MIT) against
fbs-core. The upstream suite is **not vendored** (large, moves fast); the
runner clones it into a local workdir on first use.

## What’s in git vs local-only

| Path | In git? | Purpose |
|------|---------|---------|
| `run.sh` | yes | Build/start fbs-core, provision keys, run suite |
| `report.sh` | yes | Turn a run log into a feature checklist |
| `markers.core` | yes | Default pytest filter (machine-readable drop list) |
| `markers.md` | yes | **Why** each marker is dropped or kept |
| `patches/` | yes | Cleanup patch for non-versioning servers |
| `results/checklist.md` | yes | Shared feature checklist (markdown) |
| `results/checklist.html` | yes | Shared feature checklist (browser) |
| `results/last-run.log` | **no** (gitignored) | Full pytest log (regenerated locally) |
| `.workdir/` | **no** (gitignored) | Cloned suite, venv, temp server, DB |

## Prerequisites

- `go`, `git`, `curl`, `jq`, `python3` (+ `venv`)
- Network access once to clone `ceph/s3-tests` and install Python deps

## Team quick start

From the **repository root**:

```bash
# 1) Run the core compatibility suite (managed temporary server)
./compat/s3-tests/run.sh --keep 2>&1 | tee compat/s3-tests/results/last-run.log

# 2) Build / refresh the feature checklist from that log
./compat/s3-tests/report.sh compat/s3-tests/results/last-run.log

# 3) View results
#    - Markdown: compat/s3-tests/results/checklist.md
#    - Browser:  open compat/s3-tests/results/checklist.html
xdg-open compat/s3-tests/results/checklist.html   # optional
```

First run is slower (clone + tox env). Later runs reuse `.workdir/`.

### What the runner does

1. Clones/updates `ceph/s3-tests` under `compat/s3-tests/.workdir/s3-tests`
2. Applies our cleanup patch (versioning-less teardown)
3. Builds fbs-core and starts it on a free localhost port
4. Bootstraps an **admin** (main) user and creates **member** alt/tenant keys
5. Writes `s3tests.conf` and runs pytest via tox with `markers.core`
6. Stops the server (use `--keep` to retain data/logs under `.workdir/`)

### Useful variants

```bash
# Single test
./compat/s3-tests/run.sh -- s3tests/functional/test_s3.py::test_bucket_list_empty

# No marker filter (many expected failures)
./compat/s3-tests/run.sh --full

# Against an already-running server
export FBS_S3_TESTS_ENDPOINT=http://127.0.0.1:9000
export FBS_S3_TESTS_MAIN_ACCESS_KEY='fbsv4_...'
export FBS_S3_TESTS_MAIN_SECRET_KEY='...'
export FBS_S3_TESTS_ALT_ACCESS_KEY='fbsv4_...'
export FBS_S3_TESTS_ALT_SECRET_KEY='...'
./compat/s3-tests/run.sh --external
```

Main key should be **admin**. Alt should be a different user (member is fine).

## Reading the checklist

`results/checklist.md` / `checklist.html` group tests by feature area:

- Bucket ops, put/get/delete, list, multipart, copy, …
- ACL, policy, versioning, object lock (mostly expected gaps today)

| Result | Meaning |
|--------|---------|
| pass | Matches suite expectation for that case |
| fail | Bug **or** intentional product gap |
| error | Fixture/setup failure (e.g. IAM API missing) |

There is no official “% S3 compatible” score. Use the checklist to track
progress; use `plan/s3-compatibility.md` for product claims.

## Core filter (`markers.core` / `markers.md`)

Default `--core` mode deselects large clusters we do not claim as AWS-compatible
today. Full rationale lives in **[`markers.md`](./markers.md)**.

| Bucket | Examples | Intent |
|--------|----------|--------|
| **Noise** | `fails_on_aws` | Upstream known-bad; not our gap tracker |
| **Permanent non-goal** | `bucket_policy`, `iam_*`, STS, `abac_test`, `auth_aws2` | AWS control-plane shapes we refuse; mini-IAM is grants via Management API, not these APIs |
| **Out of scope** | `storage_class`, `s3select`, cloud tier, `s3control` | Single-node / no external services |
| **Deferred** | `versioning`, `lifecycle*`, `object_lock`, `tagging`, encryption, website, logging, `sns` | Not implemented; re-enable markers when the feature ships |

**Keep (default suite focus):** data-plane CRUD, list, copy, multipart, SigV4 —
the surface described under “Implemented” in `plan/s3-compatibility.md`.

**Important:** landing mini-IAM grants does **not** mean turning on AWS
`iam_*` / `bucket_policy` markers. Progress for access control is Management
grant tests + S3 `AccessDenied` black-box coverage under `go test`.

Many tests are **unmarked** in upstream and still run under `--core`; they will
fail if the feature is missing. That is useful discovery, not a red CI gate.

Use `--full` to drop the marker filter entirely.

## Environment

| Variable | Meaning |
|----------|---------|
| `FBS_S3_TESTS_WORKDIR` | Override workdir (default `.workdir/`) |
| `FBS_S3_TESTS_REPO` | Suite git URL |
| `FBS_S3_TESTS_REF` | Suite git ref (default `master`) |
| `FBS_S3_TESTS_TOX_RECREATE=1` | Force `tox -r` |
| `FBS_S3_TESTS_ENDPOINT` | External server URL |
| `FBS_S3_TESTS_MAIN_*` / `ALT_*` / `TENANT_*` | External SigV4 keys |

## Relation to `go test`

| Suite | Role |
|-------|------|
| `go test ./...` | CI source of truth for **claimed** fbs-core behavior |
| `compat/s3-tests` | Shared team tool for AWS-like gap discovery |

Do not treat s3-tests as required CI unless the team later gates on a
known-good subset.

## License

ceph/s3-tests is MIT. Running it does not change fbs-core’s license.
Do not copy upstream test bodies into this repo unless you intend to
maintain them under this project’s license.
