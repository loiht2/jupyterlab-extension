#!/bin/sh
set -eu

#--- helpers ---------------------------------------------------------------

is_char_and_readable() {
  # returns true if $1 is a character device and is readable (false if blocked by cgroup)
  [ -c "$1" ] || return 1
  # reads 0 bytes to test access permissions without blocking
  head -c 0 < "$1" >/dev/null 2>&1
}

has_any_readable() {
  # accepts a glob list and returns true if any path exists and is readable
  for p in "$@"; do
    # safely expand glob: if no match, $p remains unchanged (contains '*')
    case "$p" in
      *'*'* ) continue ;;  # skip non-expanded globs
      *'?'* ) continue ;;
      *'['* ) continue ;;
    esac
    is_char_and_readable "$p" && return 0
  done
  return 1
}

detect_nvidia() {
  # requires nvidiactl and at least one readable nvidiaN
  is_char_and_readable /dev/nvidiactl || return 1
  # try nvidia0..nvidia31 (using glob)
  set -- /dev/nvidia[0-9] /dev/nvidia[1-9][0-9]
  has_any_readable "$@" || return 1
  # (optional) if nvidia-smi exists and is executable, provides extra confirmation
  if command -v nvidia-smi >/dev/null 2>&1; then
    nvidia-smi -L >/dev/null 2>&1 || true
  fi
  return 0
}

detect_rocm() {
  # requires kfd and at least one readable renderD*
  is_char_and_readable /dev/kfd || return 1
  # render nodes
  set -- /dev/dri/renderD*
  has_any_readable "$@" || return 1
  return 0
}

#--- main detection --------------------------------------------------------
has_gpu=false
if detect_nvidia || detect_rocm; then
  has_gpu=true
fi

#--- output ---------------------------------------------------------------
POD_NAME=${POD_NAME:-$(hostname)}
POD_NAMESPACE=${POD_NAMESPACE:-$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace 2>/dev/null || echo unknown)}
OUT_DIR="${RUNTIME_DIR:-/tmp/runtime-cfg/}"
mkdir -p "$OUT_DIR" 2>/dev/null || true

cat > "${OUT_DIR}/runtime-config.json" <<EOF
{
  "usingGPU": ${has_gpu},
  "PodName": "${POD_NAME:-unknown}",
  "PodNamespace": "${POD_NAMESPACE:-unknown}"
}
EOF

chmod 0644 "${OUT_DIR}/runtime-config.json"