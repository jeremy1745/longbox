#!/usr/bin/env bash
# Deploy a freshly-built longbox.exe to the Windows server at
# 192.168.1.163. The SMB share E:\ is expected to be mounted at
# /Volumes/192.168.1.163; the running install lives at /longbox-src.
#
# Sequence:
#   1. Copy the new exe alongside the running one as longbox.exe.new.
#   2. POST /api/v1/admin/shutdown — running process exits cleanly,
#      releasing the Windows file lock on longbox.exe.
#   3. Poll /api/v1/auth/status until it stops responding (process
#      really gone) — at most 30s.
#   4. Atomically swap longbox.exe.new → longbox.exe.
#   5. Poll /api/v1/auth/status again, expecting the auto-relaunch
#      wrapper (longbox-run.bat) to bring the new binary up — at most
#      45s. If the server doesn't come back, the wrapper isn't running
#      and the user has to relaunch manually.
#
# Exit codes:
#   0  binary swapped, new server confirmed up
#   1  build artifact missing or unreachable share
#   2  shutdown POST failed
#   3  old process still alive after 30s
#   4  swap failed
#   5  new process didn't come back up within 45s (manual relaunch needed)

set -euo pipefail

SHARE_DIR="/Volumes/192.168.1.163/longbox-src"
LOCAL_EXE="/Users/jeremy/Projects/longbox/longbox.exe"
REMOTE_EXE="${SHARE_DIR}/longbox.exe"
STAGED_EXE="${SHARE_DIR}/longbox.exe.new"
SERVER_BASE="http://192.168.1.163:22526"

log() { printf '[deploy] %s\n' "$*"; }
fail() { printf '[deploy] FAIL: %s\n' "$*" >&2; exit "${2:-1}"; }

[[ -f "$LOCAL_EXE" ]] || fail "missing $LOCAL_EXE — run 'make windows' first" 1
[[ -d "$SHARE_DIR" ]] || fail "share not mounted at $SHARE_DIR" 1

LOCAL_HASH=$(shasum -a 256 "$LOCAL_EXE" | awk '{print $1}')
log "local  exe sha256: ${LOCAL_HASH:0:12}…  ($(stat -f '%z' "$LOCAL_EXE") bytes)"
if [[ -f "$REMOTE_EXE" ]]; then
    REMOTE_HASH=$(shasum -a 256 "$REMOTE_EXE" | awk '{print $1}')
    log "remote exe sha256: ${REMOTE_HASH:0:12}…  ($(stat -f '%z' "$REMOTE_EXE") bytes)"
    if [[ "$LOCAL_HASH" == "$REMOTE_HASH" ]]; then
        log "remote already matches local — nothing to deploy"
        exit 0
    fi
fi

log "staging new exe at longbox.exe.new"
cp "$LOCAL_EXE" "$STAGED_EXE"

log "requesting shutdown via $SERVER_BASE/api/v1/admin/shutdown"
SHUTDOWN_BODY=$(mktemp)
SHUTDOWN_CODE=$(curl -sS -m 5 -o "$SHUTDOWN_BODY" -w '%{http_code}' \
    -X POST "$SERVER_BASE/api/v1/admin/shutdown" || echo "000")
if [[ "$SHUTDOWN_CODE" != "200" ]]; then
    log "shutdown returned $SHUTDOWN_CODE: $(cat "$SHUTDOWN_BODY")"
    rm -f "$SHUTDOWN_BODY" "$STAGED_EXE" || true
    fail "shutdown POST rejected (HTTP $SHUTDOWN_CODE)" 2
fi
rm -f "$SHUTDOWN_BODY"

log "waiting for old process to exit (≤30s)"
for i in $(seq 1 30); do
    if ! curl -sS -m 2 -o /dev/null "$SERVER_BASE/api/v1/auth/status" 2>/dev/null; then
        log "  process exited after ${i}s"
        break
    fi
    sleep 1
done
if curl -sS -m 2 -o /dev/null "$SERVER_BASE/api/v1/auth/status" 2>/dev/null; then
    fail "old process still responding after 30s — file lock not released" 3
fi

# Brief grace period for Windows to fully release the file handle.
sleep 1

log "swapping longbox.exe.new → longbox.exe"
mv -f "$STAGED_EXE" "$REMOTE_EXE" || fail "rename failed (file still locked?)" 4

log "waiting for new process to come up (≤45s)"
for i in $(seq 1 45); do
    if curl -sS -m 2 -o /dev/null "$SERVER_BASE/api/v1/auth/status" 2>/dev/null; then
        log "  new process responding after ${i}s"
        log "deploy complete."
        exit 0
    fi
    sleep 1
done

cat <<EOF >&2
[deploy] new process did NOT come back up within 45s.
[deploy] longbox.exe was successfully replaced — the swap is committed.
[deploy] To finish: relaunch longbox.exe on the server manually.
[deploy] To make this fully automatic going forward, copy
[deploy]   scripts/longbox-run.bat
[deploy] to the server and run it once instead of longbox.exe directly;
[deploy] it auto-relaunches whenever the binary exits (e.g. after a deploy).
EOF
exit 5
