# fbs-core Web Interface Plan

## Goal

Build a Svelte-based web interface for `fbs-core` that feels fast, obvious to use, and easy to extend while the backend is still evolving.

The frontend should follow the coding style and project conventions from `/home/radhey/code/my-sveltekit-template`, but remove template-specific packages and code that are not needed for `fbs-core`.

## Current Context

### Backend status today

- `fbs-core` currently exposes:
  - `GET /healthz` and `GET /readyz`
  - **S3 API:** `PUT /{bucket}` (CreateBucket), `GET /{bucket}?list-type=2` (ListObjectsV2), `PUT /{bucket}/*`, `GET /{bucket}/*`, `HEAD /{bucket}/*`, `DELETE /{bucket}/*`
  - **Authentication:** AWS SigV4 and Bearer Token (via `--dev` flag for local bypass)
- The Management API described in `spec/features.md` under `F10` is not implemented yet.
- Repository layers already exist for:
  - buckets
  - objects
  - users
  - multipart uploads
- The S3 API surface is now stable enough to build against. The frontend should use S3 endpoints for data operations and reserve Management API calls for admin-only features (metrics, key management) as they land.

### Template status today

- The Svelte template uses modern Svelte 5 runes, Tailwind v4, TypeScript, and a fairly minimal file structure.
- The template also includes app-specific tooling that is not needed here:
  - Convex
  - Clerk
  - Effect runtime integration
  - Vercel-specific adapter choice
  - demo app routes and wrappers

## Proposed Project Shape

Create the web dashboard as a dedicated app in a separate repository:

- `fbs-web`

Reasoning:

- keeps frontend and backend independently versioned and deployable
- avoids mixing Svelte/TypeScript code into the Go codebase
- makes CI, releases, and team ownership cleaner
- the backend API surface is now stable enough for a real client

## Template Reuse Strategy

Use the Svelte template as the starting structure, but keep only the parts that still help after removing backend-specific demo code.

### Keep

- SvelteKit + TypeScript base structure
- Tailwind setup
- ESLint + Prettier setup
- Svelte 5 runes-based component style
- simple route/layout organization

### Remove

- `convex`, `convex-svelte`, `convex-helpers`, `convex-vite-plugin`
- `@clerk/*`
- `effect`, `@effect/platform-node`
- demo routes, wrappers, stores, and generated Convex files
- template env assumptions tied to Convex/Clerk

### Replace or simplify

- replace `@sveltejs/adapter-vercel` with a deployment option better suited to this app
- simplify `vite.config.ts` to just SvelteKit + Tailwind + any genuinely useful local dev helpers

## Recommended Frontend Architecture

### App model

Use a client-driven admin dashboard with a thin API layer.

Core pieces:

- `lib/config/`: backend URL and token handling
- `lib/http/`: typed fetch wrapper and error normalization
- `lib/api/`: management API calls grouped by domain
- `lib/components/`: reusable UI primitives and feature components
- `routes/`: pages for setup, dashboard, buckets, objects, and credentials

### State model

- prefer local component state first
- use small shared state only for connection config and active selection
- avoid large global stores unless they solve a real cross-route need
- keep API responses normalized enough for UI reuse, but do not over-engineer caching early

### Rendering model

- use SSR only where it helps
- likely disable SSR for authenticated dashboard routes that depend on browser-stored backend config
- keep the shell lightweight and load feature data only when needed

## UI Scope

The first usable dashboard should focus on the flows that matter most for an admin.

### Phase 1 UI

1. Connection setup
2. Backend health and readiness status
3. Bucket list and creation
4. Object browser for a selected bucket
5. Basic object details
6. Credentials or access key management

### Phase 2 UI

1. Metrics overview
2. Multipart upload visibility
3. Bucket deletion flows
4. Search, filtering, and pagination refinement

## Route Plan

- `/`
  - product landing + quick connect
- `/app`
  - dashboard overview
- `/app/buckets`
  - bucket list and summary
- `/app/buckets/[bucket]`
  - object browser for a bucket
- `/app/keys`
  - credentials management
- `/app/settings`
  - backend URL, token, and connection preferences

## UX Priorities

### Intuitiveness

- connection state should always be visible
- bucket and object navigation should feel like a file browser, not raw JSON tables
- destructive actions need confirmation and clear feedback
- empty states should guide the next action
- loading states should be calm and predictable, not jumpy

### Performance

- keep the initial bundle small by removing unused template dependencies
- avoid heavy charting libraries initially; prefer lightweight SVG or simple stat cards
- fetch only the active view's data
- use keyed lists and incremental rendering for object rows
- keep derived UI state computed locally instead of adding unnecessary abstractions

## Backend Integration Plan

Because the Management API is still under development, the frontend should not hard-code unstable assumptions directly into pages.

### Step 1

Define a small frontend-facing API contract based on the existing product spec:

- health
- buckets
- objects
- users or keys
- metrics

### Step 2

Implement the API client behind domain functions so route components never call raw `fetch` directly.

### Step 3

Start with currently available endpoints:

- `/healthz`
- `/readyz`

### Step 4

For unfinished backend endpoints, use one of these during development:

- temporary mocked responses behind a dev-only layer
- feature-gated incomplete screens with clear placeholders

Preferred approach: feature-gated screens over fake full behavior, so the frontend stays honest about backend readiness.

## API Surface

### S3 API (available now)

The dashboard should use these endpoints for bucket and object operations:

- `PUT /{bucket}` — CreateBucket
- `GET /{bucket}?list-type=2` — ListObjectsV2
- `PUT /{bucket}/{key}` — PutObject
- `GET /{bucket}/{key}` — GetObject
- `HEAD /{bucket}/{key}` — HeadObject
- `DELETE /{bucket}/{key}` — DeleteObject

Authentication: AWS SigV4 headers or Bearer Token.

### Management API (F10 — pending)

These endpoints will be implemented in the backend for admin-only features:

- `GET /api/management/metrics`
- `GET /api/management/buckets`
- `GET /api/management/buckets/:bucket/objects`
- `GET /api/management/buckets/:bucket/objects/:key`
- `GET /api/management/keys`
- `POST /api/management/keys`
- `DELETE /api/management/keys/:id`

Exact paths may change, but the frontend should be built around these domain capabilities.

## Styling Direction

Follow the template's clean Tailwind-first style, but adapt it for an ops dashboard.

Design goals:

- quiet, dense, readable layout
- stronger hierarchy than the template demo app
- fast scanning for status, counts, and object metadata
- good mobile fallback, even if desktop is the primary admin experience

Likely visual structure:

- fixed top status bar
- left navigation on desktop
- stacked sections on mobile
- restrained color system with status colors used intentionally

## Implementation Phases

### Phase 1: App foundation

1. scaffold `tmp/fbs-web` from the template
2. remove unused packages and demo code
3. simplify config, scripts, and adapters
4. establish app shell, route structure, and shared UI primitives

### Phase 2: Connection and health

1. build settings storage for backend URL and token
2. add connection testing against existing health endpoints
3. surface connection status globally in the app shell

### Phase 3: Core browsing flows

1. add bucket list page
2. add bucket creation flow
3. add object browser page
4. add object details panel
5. add loading, error, and empty states

### Phase 4: Admin flows

1. add key management UI
2. add metrics dashboard
3. add destructive action confirmations

### Phase 5: Polish

1. tighten responsive behavior
2. reduce bundle and runtime overhead
3. improve keyboard navigation
4. run final lint, format, and type checks

## Testing Plan

Frontend work should include:

- `bun run format`
- `bun run lint`
- `bun run check`

Where practical, add targeted component or route tests for:

- connection config handling
- API error display
- bucket and object list rendering states
- destructive action confirmation behavior

## Risks and Constraints

- the Management API is not ready yet, so some UI areas cannot be fully completed against real endpoints immediately
- backend response shapes are still likely to change
- if deployment target is self-hosted, adapter choice should reflect that instead of inheriting the template's Vercel setup
- credentials handling needs careful UX and storage decisions

## Decisions To Lock Before Building

1. Confirm `fbs-web` as a separate repository.
2. Confirm adapter choice:
   - `adapter-static` if this should be a browser-only app
   - `adapter-node` or similar if server behavior is needed
3. Confirm how backend URL and token should be stored:
   - localStorage only
   - session only
   - optional persisted encrypted storage later
4. Confirm whether unfinished backend features should be hidden or shown as disabled placeholders.

## Recommended Default Decisions

- location: separate `fbs-web` repository
- rendering: client-first dashboard routes
- storage: browser localStorage for backend URL and token during early development
- incomplete features: visible but clearly marked as unavailable until backend support lands
- initial adapter: static-friendly unless a real server-side need appears

## Deliverable Definition

The first good milestone is not “everything in the spec.”

It is:

- a cleaned SvelteKit app scaffolded from the template, in its own `fbs-web` repository
- a polished app shell
- connection setup to `fbs-core`
- health status display using real endpoints
- bucket list and creation using the live S3 CreateBucket endpoint
- object browser ready to bind to S3 ListObjectsV2 and object operations
- management screens (metrics, keys) ready to bind to the Management API as it lands
