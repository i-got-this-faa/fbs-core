#!/usr/bin/env bash
# Build a feature checklist from a pytest log (or the last s3-tests run).
#
# Usage:
#   ./compat/s3-tests/report.sh
#   ./compat/s3-tests/report.sh /tmp/s3-tests-run2.log
#
# Writes:
#   compat/s3-tests/results/checklist.md
#   compat/s3-tests/results/checklist.html
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODE="core"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --core) MODE="core"; shift ;;
    --full) MODE="full"; shift ;;
    --) shift; break ;;
    *) break ;;
  esac
done
LOG="${1:-}"
WORKDIR="${FBS_S3_TESTS_WORKDIR:-${SCRIPT_DIR}/.workdir}"
OUT_DIR="${SCRIPT_DIR}/results"
MARKERS_FILE="${SCRIPT_DIR}/markers.core"
S3_TESTS_DIR="${WORKDIR}/s3-tests"
VENV_DIR="${WORKDIR}/venv"

if [[ -z "${LOG}" ]]; then
  if [[ -f /tmp/s3-tests-run2.log ]]; then
    LOG=/tmp/s3-tests-run2.log
  elif [[ -f /tmp/s3-tests-run.log ]]; then
    LOG=/tmp/s3-tests-run.log
  else
    echo "usage: $0 [pytest-or-run.log]" >&2
    echo "No default log found. Run ./compat/s3-tests/run.sh first." >&2
    exit 1
  fi
fi

[[ -f "${LOG}" ]] || { echo "log not found: ${LOG}" >&2; exit 1; }
[[ -d "${S3_TESTS_DIR}" ]] || { echo "s3-tests checkout missing; run ./compat/s3-tests/run.sh once" >&2; exit 1; }

mkdir -p "${OUT_DIR}"

# shellcheck disable=SC1091
source "${VENV_DIR}/bin/activate"

  source "${SCRIPT_DIR}/lib/marker-expr.sh"
  MARKER_EXPR="$(build_marker_expr "${MARKERS_FILE}")"

# Prefer conf from workdir if present (collect-only still reads S3TEST_CONF)
export S3TEST_CONF="${WORKDIR}/s3tests.conf"
if [[ ! -f "${S3TEST_CONF}" ]]; then
  # minimal stub so import-time config load does not crash if needed
  cat >"${S3TEST_CONF}" <<'EOF'
[DEFAULT]
host = 127.0.0.1
port = 9
is_secure = False
ssl_verify = False
[fixtures]
bucket prefix = fbs-{random}-
[s3 main]
display_name = x
user_id = x
email = x@x
access_key = x
secret_key = x
api_name = us-east-1
[s3 alt]
display_name = x
user_id = x
email = x@x
access_key = x
secret_key = x
[s3 tenant]
display_name = x
user_id = x
email = x@x
access_key = x
secret_key = x
tenant = x
[iam]
email = x@x
user_id = x
access_key = x
secret_key = x
display_name = x
[iam root]
access_key = x
secret_key = x
account_id = x
user_id = x
email = x@x
[iam alt root]
access_key = x
secret_key = x
account_id = x
user_id = x
email = x@x
EOF
fi

COLLECT="${OUT_DIR}/selected-tests.txt"
(
  cd "${S3_TESTS_DIR}"
  if [[ -x .tox/py/bin/python ]]; then
    PY=.tox/py/bin/python
  else
    PY=python
  fi
  if [[ "${MODE}" == "core" ]]; then
    "${PY}" -m pytest --collect-only -q -m "${MARKER_EXPR}" \
      s3tests/functional/test_s3.py s3tests/functional/test_headers.py s3tests/functional/test_utils.py 2>/dev/null \
      | awk '/^s3tests\// {print}' >"${COLLECT}"
  else
    "${PY}" -m pytest --collect-only -q -m "${MARKER_EXPR}" s3tests/functional 2>/dev/null \
      | awk '/^s3tests\// {print}' >"${COLLECT}"
  fi
)

python3 - "${LOG}" "${COLLECT}" "${OUT_DIR}" <<'PY'
import re, sys
from pathlib import Path
from collections import defaultdict
from datetime import datetime, timezone

log_path, collect_path, out_dir = map(Path, sys.argv[1:4])
log = log_path.read_text(errors="replace")
selected = [ln.strip() for ln in collect_path.read_text().splitlines() if ln.startswith("s3tests/")]

# Prefer a repo-relative path in generated artifacts (no local home dirs).
def portable_log_label(p: Path) -> str:
    try:
        abs_p = p.resolve()
        text = str(abs_p)
        marker = "/compat/s3-tests/"
        if marker in text:
            return "compat/s3-tests/" + text.split(marker, 1)[1]
        return p.name
    except Exception:
        return p.name

log_label = portable_log_label(log_path)

failed, errored = set(), set()
for line in log.splitlines():
    if line.startswith("FAILED s3tests/"):
        rest = line[len("FAILED "):]
        if " - " in rest:
            rest = rest.split(" - ", 1)[0]
        failed.add(rest.strip())
    elif line.startswith("ERROR s3tests/"):
        rest = line[len("ERROR "):]
        if " - " in rest:
            rest = rest.split(" - ", 1)[0]
        errored.add(rest.strip())

def _matches(selected, failed):
    # exact match
    if selected == failed:
        return True
    # bracket-suffix variance: nodeid[param]
    bracket = selected.find("[")
    if bracket >= 0 and selected[:bracket] == failed:
        return True
    return False

failed_m = {s for s in selected if any(_matches(s, f) for f in failed)}
errored_m = {s for s in selected if any(_matches(s, e) for e in errored)}
passed = [s for s in selected if s not in failed_m and s not in errored_m]

def test_name(nodeid: str) -> str:
    return nodeid.split("::", 1)[1].lower() if "::" in nodeid else nodeid.lower()

def file_name(nodeid: str) -> str:
    return nodeid.split("::", 1)[0].lower()

def category(nodeid: str) -> str:
    n, f = test_name(nodeid), file_name(nodeid)
    if "test_iam" in f:
        return "IAM / STS / OIDC"
    if "test_headers" in f:
        return "Headers / Auth edge cases"
    if "test_utils" in f:
        return "Utils / Misc"
    rules = [
        ("Versioning", [r"version"]),
        ("Object Lock / WORM", [r"object_lock", r"legal_hold", r"retention", r"obj_lock"]),
        ("ACL / Public Access", [r"acl", r"grantee", r"public_block", r"public_acl", r"ignore_public", r"authpublic", r"canned"]),
        ("Bucket Policy", [r"bucket_policy", r"policy_status", r"notprincipal", r"with_policy", r"policy_prefix", r"public_policy", r"nonpublicpolicy", r"undefined_public"]),
        ("IAM / STS / OIDC", [r"\biam\b", r"\bsts\b", r"oidc", r"webidentity", r"assume_role", r"thumbprint"]),
        ("Multipart Upload", [r"multipart", r"upload_part", r"list_parts", r"abort_multipart"]),
        ("Copy Object", [r"copy_obj", r"copy_object", r"upload_part_copy"]),
        ("Checksums", [r"checksum", r"cksum", r"content_md5", r"bad_md5", r"sha256", r"sha1", r"crc32", r"crc64"]),
        ("List Objects", [r"bucket_list", r"listv2", r"list_objects", r"delimiter", r"prefix", r"marker", r"pagination", r"encoding", r"key_count", r"list_many", r"list_empty", r"list_distinct"]),
        ("Conditional Requests", [r"ifmatch", r"ifnonematch", r"if_match", r"if_none", r"ifmodified"]),
        ("Object Attributes / Torrent", [r"object_attributes", r"torrent"]),
        ("Get / Head / Range", [r"ranged", r"range_request", r"get_object", r"head_object", r"read_unreadable", r"object_read"]),
        ("Put / Delete Object", [r"object_create", r"put_object", r"delete_object", r"multi_object_delete", r"object_write", r"object_delete"]),
        ("Bucket Ops", [r"bucket_create", r"bucket_delete", r"bucket_head", r"location", r"bucket_notexist", r"bucket_recreate"]),
        ("Tagging", [r"tagging", r"\btag_"]),
        ("Lifecycle", [r"lifecycle"]),
        ("Encryption / SSE", [r"sse_", r"encryption", r"\bkms\b"]),
        ("Website", [r"website"]),
        ("Logging", [r"logging"]),
    ]
    for label, patterns in rules:
        for p in patterns:
            if re.search(p, n):
                return label
    return "Other / Uncategorized"

order = [
    "Bucket Ops", "Conditional Requests", "Object Attributes / Torrent",
    "Put / Delete Object", "Get / Head / Range", "List Objects",
    "Multipart Upload", "Copy Object", "Checksums", "Headers / Auth edge cases",
    "ACL / Public Access", "Bucket Policy", "Versioning",
    "Object Lock / WORM", "IAM / STS / OIDC",
    "Tagging", "Lifecycle", "Encryption / SSE", "Website", "Logging",
    "Utils / Misc", "Other / Uncategorized",
]

by = defaultdict(lambda: {"pass": [], "fail": [], "error": []})
for s in selected:
    c = category(s)
    short = s.split("::", 1)[-1]
    if s in errored_m:
        by[c]["error"].append(short)
    elif s in failed_m:
        by[c]["fail"].append(short)
    else:
        by[c]["pass"].append(short)

now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
md = [
    "# S3 Compatibility Checklist",
    "",
    f"Generated from `{log_label}` ({now}).",
    "",
    "Source: ceph/s3-tests functional suite with `markers.core` filter.",
    "",
    "## Summary",
    "",
    "| Status | Count |",
    "|--------|------:|",
    f"| Passed | {len(passed)} |",
    f"| Failed | {len(failed_m)} |",
    f"| Errors | {len(errored_m)} |",
    f"| Selected | {len(selected)} |",
    f"| Pass rate | {100*len(passed)/max(len(selected),1):.1f}% |",
    "",
    "## Feature areas",
    "",
    "| Feature | Pass | Fail | Error | Rate |",
    "|---------|-----:|-----:|------:|-----:|",
]
rows = []
for c in order:
    d = by.get(c)
    if not d:
        continue
    p, f, e = len(d["pass"]), len(d["fail"]), len(d["error"])
    t = p + f + e
    if not t:
        continue
    rate = 100 * p / t
    rows.append((c, p, f, e, rate, t))
    md.append(f"| {c} | {p} | {f} | {e} | {rate:.0f}% |")

md += ["", "Legend: `[x]` passed · `[ ]` failed · `[!]` error", ""]
for c in order:
    d = by.get(c)
    if not d:
        continue
    p, f, e = len(d["pass"]), len(d["fail"]), len(d["error"])
    t = p + f + e
    if not t:
        continue
    md.append(f"## {c} ({p}/{t} passed)")
    md.append("")
    for x in sorted(d["pass"]):
        md.append(f"- [x] {x}")
    for x in sorted(d["fail"]):
        md.append(f"- [ ] {x}")
    for x in sorted(d["error"]):
        md.append(f"- [!] {x}")
    md.append("")

(out_dir / "checklist.md").write_text("\n".join(md) + "\n", encoding="utf-8")

html = [
    "<!DOCTYPE html><html><head><meta charset=utf-8><title>fbs-core S3 checklist</title>",
    """<style>
body{font-family:ui-sans-serif,system-ui,sans-serif;max-width:960px;margin:2rem auto;padding:0 1rem;background:#fafafa;color:#111}
table{border-collapse:collapse;width:100%;background:#fff;margin:1rem 0}
th,td{border:1px solid #e5e5e5;padding:.45rem .6rem} td.num{text-align:right}
.pass{color:#0a7}.fail{color:#c22}.err{color:#a60}
.bar{height:8px;background:#eee;border-radius:4px;overflow:hidden;min-width:90px}
.bar>span{display:block;height:100%;background:#12a36a}
details{background:#fff;border:1px solid #e5e5e5;border-radius:8px;padding:.75rem 1rem;margin:.75rem 0}
summary{cursor:pointer;font-weight:600}
ul{list-style:none;padding:0} li{font-family:ui-monospace,monospace;font-size:.82rem;padding:.15rem 0;border-bottom:1px solid #f0f0f0}
.badge{font-size:.75rem;background:#eee;border-radius:999px;padding:.1rem .45rem;margin-left:.4rem}
</style></head><body>""",
    "<h1>fbs-core S3 compatibility checklist</h1>",
    f"<p>From <code>{log_label}</code> · {now}</p>",
    f"<p><strong class=pass>{len(passed)} passed</strong> · <strong class=fail>{len(failed_m)} failed</strong> · <strong class=err>{len(errored_m)} errors</strong> · {len(selected)} selected ({100*len(passed)/max(len(selected),1):.1f}%)</p>",
    "<table><tr><th>Feature</th><th>Pass</th><th>Fail</th><th>Err</th><th>Rate</th><th>Coverage</th></tr>",
]
for c, p, f, e, rate, t in rows:
    html.append(f"<tr><td>{c}</td><td class='num pass'>{p}</td><td class='num fail'>{f}</td><td class='num err'>{e}</td><td class=num>{rate:.0f}%</td><td><div class=bar><span style='width:{rate:.1f}%'></span></div></td></tr>")
html.append("</table>")
for c in order:
    d = by.get(c)
    if not d:
        continue
    p, f, e = len(d["pass"]), len(d["fail"]), len(d["error"])
    t = p + f + e
    if not t:
        continue
    html.append(f"<details open><summary>{c} <span class=badge>{p}/{t} passed</span></summary><ul>")
    for x in sorted(d["pass"]):
        html.append(f"<li class=pass>✓ {x}</li>")
    for x in sorted(d["fail"]):
        html.append(f"<li class=fail>○ {x}</li>")
    for x in sorted(d["error"]):
        html.append(f"<li class=err>! {x}</li>")
    html.append("</ul></details>")
html.append("</body></html>")
(out_dir / "checklist.html").write_text("\n".join(html) + "\n", encoding="utf-8")

print(f"Wrote {out_dir / 'checklist.md'}")
print(f"Wrote {out_dir / 'checklist.html'}")
for c, p, f, e, rate, t in rows:
    print(f"  {rate:3.0f}%  {p:3d}/{t:<3d}  {c}")
PY

echo ""
echo "Open the checklist:"
echo "  markdown:  ${OUT_DIR}/checklist.md"
echo "  browser:   ${OUT_DIR}/checklist.html"
echo "  e.g.       xdg-open ${OUT_DIR}/checklist.html"
