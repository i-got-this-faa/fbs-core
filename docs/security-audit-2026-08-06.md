# Security Audit — fbs-core (2026-08-06)

Scope: full HTTP surface (cmd/server, internal/{auth,s3,management,setup,publicread,http,server,config}),
storage layer (internal/storage), metadata/SQLite layer (internal/metadata, migrations), authz engine
(internal/authz), XML/checksum handling, Dockerfile, CI workflow, docs. Method: full manual code review of
every non-test Go file, plus a **live exploit campaign** against a locally built instance (go1.26.5) —
SigV4 signing/presign tampering, path traversal, header injection (raw-socket obs-fold/obs-text/CRLF),
conditional writes, grant-scope confusion, body-size bombs, public-URL tampering, dev-mode behavior.
Tooling: `go vet` clean, `go test ./...` all pass, `govulncheck ./...` clean (0 reachable vulns in deps).

This audit deliberately re-verified every claim from the older `sec-audit.md` against the current code and
live behavior rather than trusting it.

---

## Verdict

Authentication (SigV4 + Bearer), the public-read HMAC signer, path-traversal defense, SQL parameterization,
and grant evaluation are solid — all were attacked live and held. The exploitable issues cluster in four
areas:

1. **Unbounded request bodies** in `CreateBucket` (any authenticated user) and `CompleteMultipartUpload`
   (any writer) → remote memory-exhaustion DoS.
2. **Broken conditional-write contract** on `PutObject`/`CopyObject` (If-Match / If-None-Match silently
   ignored) → silent data-loss for clients that rely on CAS semantics (locks, idempotent uploads).
3. **Trust-boundary assumptions about loopback** (setup bootstrap + dev mode) that collapse behind any
   same-host reverse proxy → unauthenticated admin bootstrap / full auth bypass.
4. **Same-origin serving of attacker-controlled `Content-Type`** with no `nosniff`/CSP/`Content-Disposition`
   → stored active content, chainable through signed public URLs.

No unauthenticated remote exploit exists in the recommended deployment (direct bind, no proxy, no dev mode).

---

## Findings

### H-1. CreateBucket reads the entire request body into memory — DoS by *any* authenticated principal (High)

**Where:** `internal/s3/bucket_create.go` (`validateCreateBucketConfiguration`, `io.ReadAll(body)`), invoked
from `CreateBucket`. Route: `PUT /{bucket}`.

**Why it's exploitable:**
- `CreateBucket` requires only authentication — `authz.Evaluator.Allow` returns `true` for
  `s3:CreateBucket` for **any** authenticated principal (`internal/authz/evaluator.go`), so a zero-grant
  `member` account can hit it.
- The body is slurped with `io.ReadAll` with no size cap before any parsing.
- `ReadTimeout` (15s default) bounds duration, not bytes — a fast client moves 100s of MB in that window,
  and requests can be parallelized.

**Live evidence:** `PUT /bigbodybucket` with a 300 MB body → server RSS went **16 MB → 1561 MB**
(response 400 only after the full read). A handful of concurrent requests OOM-kills the process.

**Fix:** `io.LimitReader(body, 4<<10)` (the XML config is a few hundred bytes), reject oversize
`Content-Length` up front, and close the body.

### H-2. CompleteMultipartUpload XML body is unbounded — memory/CPU DoS by any prefix writer (High)

**Where:** `internal/s3/multipart_handlers.go` (`xml.NewDecoder(r.Body).Decode(&req)`), no `LimitReader`.

**Why it's exploitable:** any principal with `s3:PutObject` on a key (or bucket owner) can POST an
arbitrarily large `<CompleteMultipartUpload>` document. The decoder builds the full `[]CompletePart` slice
in memory, then the handler dedups and sorts it. Contrast: `DeleteObjects` correctly caps at 2 MiB
(`internal/s3/delete_objects.go`).

**Live evidence:** 182 MB XML (2M `<Part>` elements) accepted and parsed for 8.7 s before a 400
(`InvalidPart`) — memory churn + ~9 s CPU per request, repeatable and parallelizable.

**Fix:** `io.LimitReader(r.Body, 1<<20)` (max legit doc: 10 000 parts × ~100 B), mirror the DeleteObjects
pattern; reject oversize `Content-Length` up front.

### H-3. `PutObject` and `CopyObject` silently ignore `If-Match` / `If-None-Match` — conditional writes fail open (High for data integrity)

**Where:** `internal/s3/object_handlers.go` `PutObject` (loads `oldObj`, never evaluates conditions),
`internal/s3/copy_object.go` `CopyObject` (loads `oldDestinationObject`, never evaluates conditions).
`checkPreconditionFailed` exists but is only called from `UploadPart` (advisory) and atomically in
`CompleteMultipartUpload` (`internal/metadata/multipart.go` `CompleteUpload`).

**Live evidence:**
- `PUT /testbucket/lock.txt` with `If-None-Match: *` on an existing key → **200, object overwritten**
  (S3 contract: 412).
- `PUT /testbucket/dest.txt` + `x-amz-copy-source` with `If-None-Match: *` on existing dest → **200**.
- Multipart complete with `If-None-Match: *` on the same key → **412** (correct) → behavior differs by
  upload path.

**Exploit chain:** modern AWS SDKs use `If-None-Match: *` as create-if-absent (distributed lock files,
idempotent writes, read-modify-write mutexes). Two concurrent writers both "win"; last write silently
destroys the first. Any application that moved from real S3 to fbs loses its mutual-exclusion primitive
**with no error signal**.

**Fix:** pass the conditions into `Objects.Create`/copy path and evaluate them in the same
`BEGIN IMMEDIATE` transaction as the upsert (the `CompleteUpload` pattern already exists — reuse it).
Handler-side-only checks would leave a check-then-act TOCTOU (see L-1).

### M-1. Loopback trust boundary breaks behind a reverse proxy: remote admin bootstrap & dev-mode bypass (Medium; Critical in the proxy deployment)

**Where:** `internal/setup/handlers.go` `isLoopbackRequest` trusts `r.RemoteAddr`;
`internal/auth/dev.go` `DevAuthenticator` returns an **admin principal for any request** and
`ValidateDevMode` only constrains the *bind address*; nothing checks `RemoteAddr` per request.

**Exploit chain A (bootstrap race):** deploy fbs behind nginx/traefik on the same host (a documented
setup — `docs/operations.md` "Reverse Proxy Notes"). `RemoteAddr` is now always `127.0.0.1`, so
`GET /api/setup/status` + `POST /api/setup/bootstrap` are reachable **by the entire internet**. On any
fresh database (first deploy, DB reset, node re-provision), an attacker polling `/api/setup/status` can
bootstrap *their* admin before the operator does → full instance takeover (bearer token + SigV4 secret in
the response). The atomic `CreateFirstUser` transaction prevents a *second* admin, not the *first*.

**Exploit chain B (dev mode):** `FBS_DEV=true` binds loopback (startup gate passes), operator puts a proxy
in front "temporarily" → every request from anywhere is an unauthenticated admin (verified live: `PUT
/{bucket}` 200, `GET /api/management/keys` 200 with no credentials).

**Fix:** treat `RemoteAddr` as untrusted unless explicitly configured: add a `trusted-proxies` config;
when a proxy is in use, gate setup/dev on verified forwarded headers **and** require an explicit
`FBS_BEHIND_PROXY` acknowledgement; refuse or fatally warn on `FBS_DEV` combined with proxy deployments.
Document that dev mode + proxy = unauthenticated admin.

### M-2. Stored content served same-origin with attacker-controlled `Content-Type`; no `nosniff`, CSP, or `Content-Disposition` (Medium)

**Where:** `internal/s3/object_read.go` `setObjectHeaders` + `http.ServeContent`; public path
`PublicReadObject` shares it.

**Live evidence:** `PUT evil.html` (`Content-Type: text/html`, `<script>alert(document.domain)</script>`);
`GET` returns `Content-Type: text/html` and **no** `X-Content-Type-Options`, `Content-Disposition`, or CSP.

**Exploit chain:** any principal with a write grant on any bucket uploads HTML/SVG/JS. Delivery vector is
built in: SigV4 presigned GET URLs and admin-issued `/public/...` signed URLs serve without further auth.
Victim opens the link → script executes **in the fbs origin**. Impact depends on deployment:
- worst case (fbs behind an authenticating proxy that injects identity from cookies/SSO, or a future
  console served from the same origin): full session/account takeover of any user who clicks;
- baseline: defacement/phishing on the operator's trusted storage domain, cache poisoning
  (`Cache-Control: public` on public URLs), and reflecting attacker-chosen `x-amz-meta-*` headers to
  anonymous viewers.

**Fix:** on the data plane send `X-Content-Type-Options: nosniff`, a restrictive CSP
(e.g. `sandbox; default-src 'none'`), and consider `Content-Disposition: attachment` by default with an
opt-in inline allowlist; ideally serve object bytes from a separate origin.

### M-3. No object-size / part-size / bucket-count quotas — disk exhaustion by any writer (Medium)

**Where:** `PutObject` (`object_handlers.go`) and `UploadPart` (`multipart_handlers.go`) stream
`r.Body` to disk with no cap; `CreateBucket`/`CreateMultipartUpload` create unbounded metadata rows;
`object_activity` grows forever.

**Details:** any principal with a write grant can fill the disk (per-connection bounded only by the 15 s
read window; parallel connections multiply throughput). Orphan files are handled by startup reconcile,
but raw capacity is not. Multi-tenant abuse has no circuit breaker.

**Fix:** configurable max object size / per-bucket quota / per-user bucket limit; enforce
`Content-Length` presence and cap on PUT; cap activity rows (retention job).

### M-4. Grant key-prefix matching has no path-segment boundary — `docs` also grants `docs2/...` (Medium-low; documented semantics, dangerous default)

**Where:** `internal/authz/grant.go` `PrefixMatches` / `ListPrefixCovered` — literal string prefix.

**Live evidence:** grant `s3:GetObject` prefix `docs` on bucket `data` → member read
`data/docs2/secret.txt` → **200** (outside the folder an admin intuitively granted).

`plan/access-control.md` documents literal prefixes (AWS `s3:prefix` behaves the same), so this is
"working as specified" — but the management API accepts and normalizes nothing, and one missing `/`
silently widens scope to every sibling key sharing the stem.

**Fix:** in `validateGrantPrefix` (`internal/management/grants.go`), warn or require a trailing `/` for
folder-style prefixes (or auto-normalize `docs` → `docs/`); surface exact-match vs prefix semantics in
the API docs.

### L-1. CopyObject conditional-destination check would still be check-then-act (Low-Medium)

**Where:** `internal/s3/copy_object.go` — destination read, then unconditional `Objects.Create`.

Even after fixing H-3, a handler-level precondition check followed by a separate upsert leaves a TOCTOU
window (two concurrent conditional copies to one destination both pass). Fix together with H-3 by pushing
conditions into the SQL transaction (`CompleteUpload` shows the pattern).

### L-2. DeleteObject races PutObject → a committed PUT's metadata can be deleted (Low-Medium, availability/integrity)

**Where:** `internal/objectops/cleanup.go` `DeleteObject`: `GetByKey` → `Delete` → `disk.Delete`.

Sequence: DEL reads row (file F1) → PUT upserts row (now F2, deletes F1) → DEL deletes the row (which now
points at F2) and removes F1 (already gone). Result: PUT returned 200 but the object row is gone; F2
orphaned until next restart reconcile. Same shape for `EmptyBucket` vs concurrent PUT, and management
`DeleteBucket` (FK error → 500 instead of `BucketNotEmpty`).

**Fix:** make delete conditional on identity: `DELETE ... WHERE bucket=? AND key=? AND storage_path=?`
(fail/refresh on 0 rows), or take a per-key lock; map FK errors in management delete to 409.

### L-3. Bucket name `api` is not reserved (Low)

**Where:** `validateBucketName` (`internal/s3/bucket_create.go`) reserves only `public`.

**Live evidence:** `PUT /api` → 200. Chi's static-over-wildcard precedence keeps `/api/setup/*` and
`/api/management/*` working (verified live), but bucket `api` keys under `setup/` or `management/` are
unreachable, the namespace is confusing, and any *future* root-level route creates a real shadow.
Reserve `api` (and any planned first segments) now — it is a one-line guard that becomes breaking later.

### L-4. Object-key encoding is inconsistent — `%2F`-class keys are stored escaped, everything else decoded (Low; correctness with fail-closed authz side effect)

**Where:** chi wildcard params come from `EscapedPath` when `RawPath != ""` (client percent-encoded
something Go would not), else from decoded `Path`.

**Live evidence:** `PUT /testbucket/..%2F..%2Ffbs.db` stored key `..%2F..%2Ffbs.db` (encoded), while
`PUT /testbucket/my%20file.txt` stored `my file.txt` (decoded). Consequences: (a) the same logical key
can exist twice in different encodings; (b) grant prefixes match whichever form was stored; (c) signed
public URLs for escaped-form keys 404 (signature verifies, lookup misses). All observed failure modes are
fail-closed (deny/404) — correctness/compat, not a bypass — but it will confuse grant audits.

**Fix:** normalize keys once at the handler boundary (single canonical decode with traversal re-check)
and store/compare only that form.

### L-5. Dev mode is drive-by writable from a developer's browser (DNS rebinding / blind cross-origin POSTs) (Low)

**Where:** `DevAuthenticator` accepts everything; no `Host`/`Origin`/`Sec-Fetch-*` check anywhere;
JSON decoders don't require `Content-Type: application/json`.

A malicious page in the developer's browser can issue simple `POST`s to `http://127.0.0.1:9000/...`
(CORS blocks *reading*, not *sending*): create buckets, create multipart uploads, create blind admin keys
(response unreadable to the attacker), etc.

**Fix:** in dev mode require a matching `Host: 127.0.0.1|localhost|[::1]` and reject requests whose
`Origin` is not loopback.

### L-6. Cleartext HTTP only + `UNSIGNED-PAYLOAD` accepted (Low; deployment)

No in-process TLS. On any untrusted network: bearer tokens and presigned URLs are sniffable, and
`UNSIGNED-PAYLOAD` (header or presign) lets a MITM swap bodies undetected (the payload hash is signed
only when the client chooses to send it). The body hash is likewise not verified server-side on
`CompleteMultipartUpload`/copy paths.

**Fix:** offer built-in TLS (or document a TLS-terminating proxy as *required*); consider refusing
`UNSIGNED-PAYLOAD` on cleartext listeners.

### L-7. Governance nits (Low/Info)

- Presigned SigV4 URLs and public signed URLs are irrevocable while the user stays active; no
  credential-rotation API exists (only delete + recreate) — document the revocation story.
- `x-amz-meta-*` values are reflected raw, including obs-text bytes (`0x80–0xFF` verified live). Go's
  parser prevents CR/LF from entering values (obs-fold collapses to a space; bare CR → 400), so response
  splitting is **not** possible — still, consider rejecting non-printable bytes at upload.
- `object_activity` has no retention (see M-3).
- Setup/bootstrap JSON responses derive `management_url`/`s3_url` from `r.Host` when `PublicBaseURL` is
  unset — loopback-gated today, but pair with the M-1 fix.
- `MetadataCache.cloneObject` shallow-copies: `UserMetadata` maps are aliased between cache entries
  (no mutator found today; add a deep copy or document the invariant).

---

## Attack chains (how the findings compose)

1. **Zero-grant member → service outage:** H-1 (CreateBucket body bomb) needs nothing but a member key;
   repeated/parallel requests OOM the single-node server. M-3 (disk fill) keeps working even after memory
   limits are added.
2. **Writer → persistent client-side compromise:** M-2 upload HTML → deliver via presigned/public URL →
   script runs in the service origin. With L-6 (cleartext) on a hostile network, links can also be
   rewritten in transit.
3. **Proxy deployment → full takeover:** M-1 chain A (bootstrap race) hands over the whole instance;
   M-1 chain B (dev mode) is instant unauthenticated admin. Either composes with H-1/H-2 for persistent
   sabotage and with H-3 to silently corrupt application state that trusts CAS semantics.
4. **Data-integrity sabotage by a concurrent writer:** H-3 (ignored `If-None-Match`) + L-1 (TOCTOU) +
   L-2 (delete/put race) let a writer with legitimate key access undermine lock-file and idempotency
   patterns of co-tenants without tripping any error.

## Verified safe (attacked live or code-proven)

- **SigV4:** valid sign/verify works; tampered body (400 via signed payload hash), tampered `X-Amz-Date`
  (401), path swap (401), presign expiry >7d (401), presign path swap (401), GET-presign used as PUT (401),
  extra query params on presign (401) — all rejected. `hmac.Equal`, ±15 min skew, date/scope match,
  `host` required in SignedHeaders, canonical query rebuilt server-side with correct URI encoding and
  sorted values.
- **Confused deputy:** SigV4 `CopyObject` with unsigned `x-amz-copy-source` → 403 (signed → 200);
  `x-amz-copy-source-range` must also be signed; bearer is exempt by design.
- **Public read signer:** path-bound HMAC, exactly-2-params rule; tampered expires / extra param / wrong
  key → 403; deleted object → 404; POST → 401/405. Secret ≥32 bytes enforced at startup.
- **Path traversal:** `%2e%2e`, `%2f`, backslash, NUL, absolute, `.`/`..` variants → 400/404; storage
  re-validates (`isWithinBase`) and all writes land on server-generated UUID paths; nothing escaped
  `DATA_DIR` in testing.
- **SQL:** every query parameterized (incl. dynamic `IN` via placeholders); no string-built values.
- **XML/XXE:** Go `encoding/xml` (no external entities); responses built with `xml.Encoder` (auto-escape).
- **Tokens:** 128-bit access key IDs, 256-bit secrets, SHA-256 at rest, constant-time compare, secrets
  scrubbed from all read paths; plaintext returned exactly once.
- **Bootstrap:** `BEGIN IMMEDIATE` + count in one tx (double-bootstrap → 409 live-verified); loopback gate
  correct for direct binds (see M-1 for the proxy caveat); strict JSON decode.
- **Dev mode:** refuses non-loopback bind (live-verified); see M-1/L-5 for what it does *not* gate.
- **Authz engine:** admin → owner → grant → default-deny; DeleteBucket never grantable; per-key authz in
  multi-delete with no existence oracle; grant CRUD CAS on ownership; transfer requires active target.
- **Middleware:** panic recovery without stack leak; request IDs server-generated; access log has no
  Authorization/query material; CORS is origin-allowlisted, credentials-scoped, never short-circuits auth;
  test routes behind `//go:build testendpoints`.
- **Deps:** `govulncheck` — 0 reachable vulnerabilities; `go vet` clean; `go test ./...` green.

## Prioritized fixes

1. Cap bodies: `CreateBucket` (H-1), `CompleteMultipartUpload` (H-2) — one-line `LimitReader`s +
   `Content-Length` pre-checks.
2. Push `If-Match`/`If-None-Match` into the write transactions for `PutObject`/`CopyObject` (H-3, L-1).
3. Send `nosniff` + CSP (+ consider `Content-Disposition`) on object reads (M-2).
4. Add `trusted-proxies` config; gate setup/dev on it; refuse `FBS_DEV` when proxying is detected (M-1, L-5).
5. Add object-size/quota limits and activity retention (M-3).
6. Reserve `api` as a bucket name; normalize grant prefixes with a trailing-`/` nudge (L-3, M-4).
7. Conditionalize `DeleteObject` on `storage_path`; map FK failures to 409 in management delete (L-2).
8. Canonicalize object-key decoding at the handler boundary (L-4); document TLS expectation (L-6).
