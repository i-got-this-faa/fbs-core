# Shared awk-based marker expression builder.
# Usage:  build_marker_expr <markers-file>
build_marker_expr() {
  awk '
    /^[[:space:]]*#/ { next }
    /^[[:space:]]*$/ { next }
    {
      gsub(/^[[:space:]]+|[[:space:]]+$/, "")
      if (n++) printf " and "
      printf "%s", $0
    }
  ' "${1}"
}
