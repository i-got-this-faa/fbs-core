#!/usr/bin/env bash
# Run ceph/s3-tests against a local fbs-core instance.
#
# Default: start a temporary server, provision SigV4 keys, run a core
# (in-scope) subset of functional tests.
#
# Usage:
#   ./compat/s3-tests/run.sh
#   ./compat/s3-tests/run.sh --full
#   ./compat/s3-tests/run.sh -- s3tests/functional/test_s3.py::test_bucket_list_empty
#   FBS_S3_TESTS_ENDPOINT=http://127.0.0.1:9000 \
#     FBS_S3_TESTS_MAIN_ACCESS_KEY=... FBS_S3_TESTS_MAIN_SECRET_KEY=... \
#     FBS_S3_TESTS_ALT_ACCESS_KEY=...  FBS_S3_TESTS_ALT_SECRET_KEY=... \
#     ./compat/s3-tests/run.sh --external
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
WORKDIR="${FBS_S3_TESTS_WORKDIR:-${SCRIPT_DIR}/.workdir}"
S3_TESTS_DIR="${WORKDIR}/s3-tests"
S3_TESTS_REPO="${FBS_S3_TESTS_REPO:-https://github.com/ceph/s3-tests.git}"
S3_TESTS_REF="${FBS_S3_TESTS_REF:-e3e1c240d}"
MARKERS_FILE="${SCRIPT_DIR}/markers.core"
SERVER_BIN="${WORKDIR}/fbs-server"
SERVER_LOG="${WORKDIR}/fbs-server.log"
CONF_PATH="${WORKDIR}/s3tests.conf"
VENV_DIR="${WORKDIR}/venv"

MODE="core"       # core | full
EXTERNAL=0
KEEP_WORKDIR=0
PYTEST_ARGS=()

log() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Run ceph/s3-tests against fbs-core.

Options:
  --core            Core marker filter (default). See markers.core.
  --full            No marker filter; run whatever is selected by pytest args.
  --external        Do not start fbs-core; use FBS_S3_TESTS_* endpoint/keys.
  --keep            Keep workdir (server data, logs, conf) after exit.
  -h, --help        Show this help.

Any arguments after -- are passed to pytest (via tox).

Environment:
  FBS_S3_TESTS_WORKDIR   Working directory (default: compat/s3-tests/.workdir)
  FBS_S3_TESTS_REPO      s3-tests git URL
  FBS_S3_TESTS_REF       git ref/branch/tag (default: master)
  FBS_S3_TESTS_ENDPOINT  External mode endpoint, e.g. http://127.0.0.1:9000
  FBS_S3_TESTS_MAIN_ACCESS_KEY / FBS_S3_TESTS_MAIN_SECRET_KEY
  FBS_S3_TESTS_ALT_ACCESS_KEY  / FBS_S3_TESTS_ALT_SECRET_KEY
  FBS_S3_TESTS_TENANT_ACCESS_KEY / FBS_S3_TESTS_TENANT_SECRET_KEY  (optional)
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --core) MODE="core"; shift ;;
    --full) MODE="full"; shift ;;
    --external) EXTERNAL=1; shift ;;
    --keep) KEEP_WORKDIR=1; shift ;;
    -h|--help) usage; exit 0 ;;
    --) shift; PYTEST_ARGS+=("$@"); break ;;
    *) PYTEST_ARGS+=("$1"); shift ;;
  esac
done

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

need_cmd git
need_cmd go
need_cmd python3
need_cmd curl
need_cmd jq

mkdir -p "${WORKDIR}"

SERVER_PID=""
cleanup() {
  local code=$?
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    log "stopping fbs-core (pid ${SERVER_PID})"
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  if [[ "${KEEP_WORKDIR}" -eq 0 && "${EXTERNAL}" -eq 0 ]]; then
    # Keep logs/conf on failure for debugging.
    if [[ "${code}" -eq 0 ]]; then
      rm -rf "${WORKDIR}/data" "${WORKDIR}/fbs.db" "${WORKDIR}/fbs.db-shm" "${WORKDIR}/fbs.db-wal" 2>/dev/null || true
    fi
  fi
  exit "${code}"
}
trap cleanup EXIT

clone_s3_tests() {
  if [[ -d "${S3_TESTS_DIR}/.git" ]]; then
    log "updating s3-tests at ${S3_TESTS_DIR} (ref ${S3_TESTS_REF})"
    git -C "${S3_TESTS_DIR}" fetch --depth 1 origin "${S3_TESTS_REF}"
    git -C "${S3_TESTS_DIR}" reset --hard FETCH_HEAD
  else
    log "cloning s3-tests (${S3_TESTS_REPO} @ ${S3_TESTS_REF})"
    rm -rf "${S3_TESTS_DIR}"
    if ! git clone --depth 1 --branch "${S3_TESTS_REF}" "${S3_TESTS_REPO}" "${S3_TESTS_DIR}"; then
      git clone --depth 1 "${S3_TESTS_REPO}" "${S3_TESTS_DIR}"
      git -C "${S3_TESTS_DIR}" fetch --depth 1 origin "${S3_TESTS_REF}" || true
      git -C "${S3_TESTS_DIR}" reset --hard FETCH_HEAD 2>/dev/null || true
    fi
  fi

  # setup/teardown empties buckets via ListObjectVersions; fbs-core returns
  # NotImplemented. Patch cleanup to fall back to ListObjectsV2.
  log "applying fbs-core cleanup patch (no versioning)"
  python3 "${SCRIPT_DIR}/patches/apply-nuke-fallback.py" \
    "${S3_TESTS_DIR}/s3tests/functional/__init__.py"
}

build_server() {
  log "building fbs-core server"
  go build -o "${SERVER_BIN}" "${REPO_ROOT}/cmd/server"
}

pick_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

wait_http() {
  local url="$1"
  local tries="${2:-60}"
  local i
  for ((i = 1; i <= tries; i++)); do
    if curl -fsS "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

create_key() {
  local base_url="$1"
  local bearer="$2"
  local display_name="$3"
  local role="$4"
  curl -fsS -X POST "${base_url}/api/management/keys" \
    -H "Authorization: Bearer ${bearer}" \
    -H "Content-Type: application/json" \
    -d "{\"display_name\":\"${display_name}\",\"role\":\"${role}\"}"
}

write_conf() {
  local host="$1"
  local port="$2"
  local main_ak="$3"
  local main_sk="$4"
  local main_uid="$5"
  local main_name="$6"
  local alt_ak="$7"
  local alt_sk="$8"
  local alt_uid="$9"
  local alt_name="${10}"
  local tenant_ak="${11}"
  local tenant_sk="${12}"
  local tenant_uid="${13}"
  local tenant_name="${14}"

  # ConfigParser-safe: values with special characters are fine as long as they
  # are not multi-line. SigV4 secrets from fbs-core are single-line.
  cat >"${CONF_PATH}" <<EOF
[DEFAULT]
host = ${host}
port = ${port}
is_secure = False
ssl_verify = False

[fixtures]
bucket prefix = fbs-{random}-
iam name prefix = fbs-s3-tests-
iam path prefix = /fbs-s3-tests/

[s3 main]
display_name = ${main_name}
user_id = ${main_uid}
email = main@fbs.local
api_name = us-east-1
access_key = ${main_ak}
secret_key = ${main_sk}

[s3 alt]
display_name = ${alt_name}
user_id = ${alt_uid}
email = alt@fbs.local
access_key = ${alt_ak}
secret_key = ${alt_sk}

[s3 tenant]
display_name = ${tenant_name}
user_id = ${tenant_uid}
email = tenant@fbs.local
access_key = ${tenant_ak}
secret_key = ${tenant_sk}
tenant = fbs

# IAM/STS sections are required by s3-tests config parsing even when those
# tests are excluded. Credentials reuse the main admin key; IAM tests fail.
[iam]
email = iam@fbs.local
user_id = ${main_uid}
access_key = ${main_ak}
secret_key = ${main_sk}
display_name = ${main_name}

[iam root]
access_key = ${main_ak}
secret_key = ${main_sk}
account_id = fbs
user_id = ${main_uid}
email = iam-root@fbs.local

[iam alt root]
access_key = ${alt_ak}
secret_key = ${alt_sk}
account_id = fbs-alt
user_id = ${alt_uid}
email = iam-alt-root@fbs.local
EOF
}

start_managed_server() {
  local port data_dir db_path
  port="$(pick_port)"
  data_dir="${WORKDIR}/data"
  db_path="${WORKDIR}/fbs.db"
  rm -rf "${data_dir}"
  rm -f "${db_path}" "${db_path}-shm" "${db_path}-wal"
  mkdir -p "${data_dir}"

  log "starting fbs-core on 127.0.0.1:${port}"
  # Generous timeouts so large multipart/object tests are not cut off by defaults.
  "${SERVER_BIN}" \
    --http-addr "127.0.0.1:${port}" \
    --data-dir "${data_dir}" \
    --db-path "${db_path}" \
    --read-timeout 5m \
    --write-timeout 10m \
    --idle-timeout 5m \
    >"${SERVER_LOG}" 2>&1 &
  SERVER_PID=$!

  if ! wait_http "http://127.0.0.1:${port}/healthz" 80; then
    [[ -f "${SERVER_LOG}" ]] && tail -n 50 "${SERVER_LOG}" >&2 || true
    die "fbs-core did not become healthy (see ${SERVER_LOG})"
  fi

  log "bootstrapping admin"
  local bootstrap
  bootstrap="$(curl -fsS -X POST "http://127.0.0.1:${port}/api/setup/bootstrap" \
    -H "Content-Type: application/json" \
    -d '{"display_name":"s3-tests main"}')"

  local bearer main_ak main_sk main_uid main_name
  bearer="$(jq -r '.bearer_token' <<<"${bootstrap}")"
  main_ak="$(jq -r '.sigv4.access_key_id' <<<"${bootstrap}")"
  main_sk="$(jq -r '.sigv4.secret_key' <<<"${bootstrap}")"
  main_uid="$(jq -r '.key.id' <<<"${bootstrap}")"
  main_name="$(jq -r '.key.display_name' <<<"${bootstrap}")"

  [[ -n "${bearer}" && "${bearer}" != "null" ]] || die "bootstrap missing bearer_token"
  [[ -n "${main_ak}" && "${main_ak}" != "null" ]] || die "bootstrap missing sigv4 access key"

  log "creating alt (member) and tenant (member) keys"
  local alt_json tenant_json
  alt_json="$(create_key "http://127.0.0.1:${port}" "${bearer}" "s3-tests alt" "member")"
  tenant_json="$(create_key "http://127.0.0.1:${port}" "${bearer}" "s3-tests tenant" "member")"

  local alt_ak alt_sk alt_uid alt_name tenant_ak tenant_sk tenant_uid tenant_name
  alt_ak="$(jq -r '.sigv4.access_key_id' <<<"${alt_json}")"
  alt_sk="$(jq -r '.sigv4.secret_key' <<<"${alt_json}")"
  alt_uid="$(jq -r '.key.id' <<<"${alt_json}")"
  alt_name="$(jq -r '.key.display_name' <<<"${alt_json}")"
  tenant_ak="$(jq -r '.sigv4.access_key_id' <<<"${tenant_json}")"
  tenant_sk="$(jq -r '.sigv4.secret_key' <<<"${tenant_json}")"
  tenant_uid="$(jq -r '.key.id' <<<"${tenant_json}")"
  tenant_name="$(jq -r '.key.display_name' <<<"${tenant_json}")"

  write_conf "127.0.0.1" "${port}" \
    "${main_ak}" "${main_sk}" "${main_uid}" "${main_name}" \
    "${alt_ak}" "${alt_sk}" "${alt_uid}" "${alt_name}" \
    "${tenant_ak}" "${tenant_sk}" "${tenant_uid}" "${tenant_name}"

  ENDPOINT_DISPLAY="http://127.0.0.1:${port}"
}

use_external_server() {
  local endpoint host port
  endpoint="${FBS_S3_TESTS_ENDPOINT:-}"
  [[ -n "${endpoint}" ]] || die "--external requires FBS_S3_TESTS_ENDPOINT"

  local main_ak="${FBS_S3_TESTS_MAIN_ACCESS_KEY:-}"
  local main_sk="${FBS_S3_TESTS_MAIN_SECRET_KEY:-}"
  local alt_ak="${FBS_S3_TESTS_ALT_ACCESS_KEY:-}"
  local alt_sk="${FBS_S3_TESTS_ALT_SECRET_KEY:-}"
  local tenant_ak="${FBS_S3_TESTS_TENANT_ACCESS_KEY:-${alt_ak}}"
  local tenant_sk="${FBS_S3_TESTS_TENANT_SECRET_KEY:-${alt_sk}}"

  [[ -n "${main_ak}" && -n "${main_sk}" ]] || die "external mode needs FBS_S3_TESTS_MAIN_ACCESS_KEY/SECRET_KEY"
  [[ -n "${alt_ak}" && -n "${alt_sk}" ]] || die "external mode needs FBS_S3_TESTS_ALT_ACCESS_KEY/SECRET_KEY"

  # Parse host/port from endpoint (http://host:port)
  local parsed
  parsed="$(python3 - <<PY
from urllib.parse import urlparse
u = urlparse("${endpoint}")
if not u.hostname:
    raise SystemExit("endpoint must include host, got: ${endpoint}")
port = u.port
if port is None:
    port = 443 if u.scheme == "https" else 80
print(u.hostname)
print(port)
PY
)"
  host="$(printf '%s\n' "${parsed}" | sed -n '1p')"
  port="$(printf '%s\n' "${parsed}" | sed -n '2p')"

  write_conf "${host}" "${port}" \
    "${main_ak}" "${main_sk}" "external-main" "external main" \
    "${alt_ak}" "${alt_sk}" "external-alt" "external alt" \
    "${tenant_ak}" "${tenant_sk}" "external-tenant" "external tenant"

  ENDPOINT_DISPLAY="${endpoint}"
}

setup_python() {
  log "preparing Python venv + tox"
  if [[ ! -d "${VENV_DIR}" ]]; then
    python3 -m venv "${VENV_DIR}"
  fi
  # shellcheck disable=SC1091
  source "${VENV_DIR}/bin/activate"
  python -m pip install -q --upgrade pip
  python -m pip install -q tox
}

source "${SCRIPT_DIR}/lib/marker-expr.sh"

run_tests() {
  # shellcheck disable=SC1091
  source "${VENV_DIR}/bin/activate"
  cd "${S3_TESTS_DIR}"

  local args=()
  if [[ "${MODE}" == "core" ]]; then
    if [[ ! -f "${MARKERS_FILE}" ]]; then
      die "missing markers file: ${MARKERS_FILE}"
    fi
    local expr
    expr="$(build_marker_expr "${MARKERS_FILE}")"
    args+=(-m "${expr}")
    # Default path: boto3 functional suite. IAM/STS modules excluded by markers
    # and by not selecting those files when no explicit pytest args are given.
    if [[ ${#PYTEST_ARGS[@]} -eq 0 ]]; then
      args+=(s3tests/functional/test_s3.py s3tests/functional/test_headers.py s3tests/functional/test_utils.py)
    fi
  fi
  if [[ ${#PYTEST_ARGS[@]} -gt 0 ]]; then
    args+=("${PYTEST_ARGS[@]}")
  elif [[ "${MODE}" == "full" ]]; then
    args+=(s3tests/functional)
  fi

  log "endpoint: ${ENDPOINT_DISPLAY}"
  log "config:   ${CONF_PATH}"
  log "mode:     ${MODE}"
  log "running:  tox -- ${args[*]}"

  export S3TEST_CONF="${CONF_PATH}"
  export S3_USE_SIGV4=1
  # tox reuses env; force recreate if requirements change via FBS_S3_TESTS_TOX_RECREATE=1
  local tox_extra=()
  if [[ "${FBS_S3_TESTS_TOX_RECREATE:-0}" == "1" ]]; then
    tox_extra+=(-r)
  fi

  local results_dir="${SCRIPT_DIR}/results"
  local run_log="${results_dir}/last-run.log"
  mkdir -p "${results_dir}"

  set +e
  tox "${tox_extra[@]}" -- "${args[@]}" 2>&1 | tee "${run_log}"
  local tox_status=${PIPESTATUS[0]}
  set -e

  if [[ -x "${SCRIPT_DIR}/report.sh" ]]; then
    log "building feature checklist → ${results_dir}/checklist.{md,html}"
    "${SCRIPT_DIR}/report.sh" "${run_log}" || log "warning: report.sh failed (checklist not updated)"
  fi

  log "run log:  ${run_log}"
  log "checklist: ${results_dir}/checklist.md  (and checklist.html)"
  return "${tox_status}"
}

clone_s3_tests
if [[ "${EXTERNAL}" -eq 1 ]]; then
  use_external_server
else
  build_server
  start_managed_server
fi
setup_python
run_tests
log "done"
