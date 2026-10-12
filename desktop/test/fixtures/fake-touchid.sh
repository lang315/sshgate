#!/bin/sh
# Fake sshgate-touchid for vitest and e2e: "seals" with base64 and asks for no
# finger. FAKE_TOUCHID_MODE names a file holding the mode: ok (default),
# cancel, invalid, unavailable, fail, slow (blocks until killed).
mode=ok
if [ -n "${FAKE_TOUCHID_MODE:-}" ] && [ -f "$FAKE_TOUCHID_MODE" ]; then mode=$(cat "$FAKE_TOUCHID_MODE"); fi
case "$1" in
  available) [ "$mode" = unavailable ] && exit 4; exit 0 ;;
  enroll) printf '{"v":1,"fake":"%s"}' "$(base64 | tr -d '\n')" ;;
  unlock)
    case "$mode" in
      cancel) exit 2 ;;
      invalid) exit 3 ;;
      unavailable) exit 4 ;;
      fail) echo boom >&2; exit 1 ;;
      slow) exec sleep 30 ;;
    esac
    sed -n 's/.*"fake":"\([^"]*\)".*/\1/p' | base64 -d ;;
  *) exit 1 ;;
esac
