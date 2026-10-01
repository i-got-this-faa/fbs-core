# Go 1.27 modernization surface ledger

Target behavior: fbs-core builds and runs on Go 1.27 while adopting useful language, standard-library, runtime, and test improvements without changing S3 or Management API contracts.

| Surface | Applies | Evidence | Planned change | Check | State |
|---|---|---|---|---|---|
| Entry points | applies | `cmd/server/main.go`; `internal/http/router.go`; process build tests in `cmd/server/main_test.go` | Pin the module, container, and development toolchain to Go 1.27 | `go build ./cmd/server`; tagged build; process startup | proved |
| Clients | applies | S3 HTTP routes, Management JSON routes, setup endpoints, signed public reads, and test-only auth endpoint | Preserve request, response, XML, JSON, auth, and signed-URL behavior while changing internals | Black-box HTTP tests; compatibility suite not run | proved |
| Providers | applies | `internal/auth`, `internal/metadata`, `internal/storage`, and `internal/publicread` | Evaluate standard `uuid`, typed `errors.AsType`, iterator APIs, and lifecycle test helpers at their real owners | Focused package checks and race checks | proved |
| Contracts | applies | `go.mod`, `Dockerfile`, `flake.nix`, `.github/workflows/go.yml`, metadata/auth/storage interfaces, JSON DTOs, and S3 XML DTOs | Update build contracts and dependency graph first; keep process and wire contracts stable | `go mod verify`; Go builds; full tests; Docker/Nix builds unavailable | proved |
| Reverse state | applies | object commit and cleanup order, multipart claim/delete flow, metadata cache invalidation, and graceful shutdown | Prove cleanup, cancellation, cache invalidation, and shutdown behavior after each refactor | Existing lifecycle tests and `go test -race`; `testing/synctest` and leak diagnostics not applicable to these I/O-heavy tests | proved |
| Connection modes | applies | dev and production auth, Bearer and SigV4, loopback setup, public signed reads, cached and uncached metadata, multipart cleanup | Verify every mode separately; do not infer production behavior from dev-mode tests | Full Go suite and server process tests; external compatibility suite not run | proved |
| Tests | applies | package tests, server process tests, tagged test endpoints, and `compat/s3-tests` | Adopt `WaitGroup.Go`, integer ranges, `strings.SplitSeq`, and `errors.AsType` only where behavior remains clear; add tests for changed contracts | Focused Go tests, full Go tests, race checks, vet, and tagged build; compatibility tests not run | proved |
| Documents | applies | `README.md`, `docs/development.md`, `docs/architecture.md`, Docker instructions, and compatibility docs | Document the supported Go 1.27 toolchain and any runtime or compatibility prerequisites | Review version references and build instructions | proved |

## Findings and decisions

- The image’s `Box[T].Map[U]` syntax is valid in Go 1.27, but this repository has no existing generic domain type or fluent value pipeline that would justify introducing `Box`.
- The direct UUID dependency is replaced by the Go 1.27 `uuid` package. `go mod tidy` correctly retains `github.com/google/uuid` as an indirect dependency of `modernc.org/sqlite` and `modernc.org/libc`.
- The safe modernization set uses `errors.AsType`, `strings.SplitSeq`, integer ranges, `min`, `sync.WaitGroup.Go`, and `httptest.NewTestServer` without changing wire contracts.
- `encoding/json/v2` is not imported directly because existing `encoding/json` already receives the Go 1.27 implementation while preserving the service’s established JSON API behavior.
- `testing/synctest` is not applied to SQLite, filesystem, or process tests because those operations are outside its deterministic bubble; the affected concurrency path uses the new test-server lifecycle and passes race checks.
- Experimental SIMD, runtime secret erasure, ML-DSA, QUIC-specific fields, and database driver scanning APIs do not have an evidence-backed use in the current service.

## Issue #32: safe download headers

Target behavior: object reads preserve the stored `Content-Type`, optionally return a `Content-Disposition` override, and send `X-Content-Type-Options: nosniff`. Allowed browser origins can read the override.

| Surface | Applies | Evidence | Change | Check | State |
|---|---|---|---|---|---|
| Entry points | applies | `internal/s3/routes.go`; `internal/s3/object_read.go` | Route S3 and signed public GET/HEAD through shared response headers | focused S3 tests; live GET/HEAD | proved |
| Clients | applies | S3 clients; browser scripts from allowed CORS origins; management-created public URLs; no in-repo download client | Add the S3 query override and expose `Content-Disposition` to allowed browser clients | presigned S3 and CORS tests; live CORS HTTP flow | proved |
| Providers | applies | `Storage.Open`; `internal/publicread/signer.go` | Keep one object read path and sign public response overrides | focused public-read and signer tests | proved |
| Contracts | applies | S3 query; `response_content_disposition`; public URL signature; CORS response headers | Preserve defaults; bind public overrides to the URL signature and expose the response header | GET/HEAD tests; tampering returns 403; CORS test | proved |
| Reverse state | does not apply | Object reads do not mutate object state | No reverse action | not applicable | closed |
| Connection modes | applies | Bearer, SigV4 presigned, custom signed public reads, and configured CORS origins | Verify each HTTP read mode, including browser header access | full tests; live local CORS HTTP flow | proved |
| Tests | applies | `internal/s3`, `internal/publicread`, `internal/management`, `internal/http` | Cover binary GET/HEAD, stored MIME, safe headers, invalid input, signatures, and CORS exposure | `go test ./...`; `go vet ./...` | proved |
| Documents | applies | `docs/s3-api.md`, `docs/management-api.md`, `docs/quickstart.md`, `docs/operations.md`, `docs/configuration.md` | Explain ordinary reads, safe response headers, forced downloads, and cross-origin header access | documentation review | proved |
