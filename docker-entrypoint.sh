#!/bin/sh
# Starts a headless-but-not---headless X server (see internal/goodreads/
# login.go for why: AWS WAF Bot Control blocks Chrome's real --headless
# mode, but a genuinely headed Chrome running against a virtual display
# passes fine), then execs straight into the real command.
#
# The exec is deliberate, not cosmetic: it replaces this script's process
# image with the target command instead of running it as a child, so the
# target becomes tini's direct child and receives SIGTERM/SIGINT directly.
# (An earlier version of this image shelled out through the xvfb-run
# wrapper script instead, which runs its target as an ordinary foreground
# job rather than exec'ing it — Ctrl-C/SIGTERM reached xvfb-run's shell
# but never made it to leafmark underneath.)
set -e

Xvfb :99 -screen 0 1280x1024x24 -nolisten tcp &
export DISPLAY=:99

# Give Xvfb a moment to create its socket before Chrome tries to connect.
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25; do
    [ -e /tmp/.X11-unix/X99 ] && break
    sleep 0.2
done

exec /usr/local/bin/leafmark "$@"
