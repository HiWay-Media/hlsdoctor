#!/bin/sh
# The repository's own invariants, run by CI and by hand. Exit 1 on the first failure.
set -eu
cd "$(dirname "$0")/.."
fail() { echo "✗ $1" >&2; exit 1; }
V="$(tr -d ' \n' < VERSION)"
echo "$V" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || fail "VERSION must be x.y.z, got '$V'"
grep -q "^## \[Unreleased\]" CHANGELOG.md || fail "CHANGELOG.md needs an [Unreleased] section"
grep -q "^## \[$V\]" CHANGELOG.md || fail "CHANGELOG.md has no section for $V"
for f in README.md CLAUDE.md CONTRIBUTING.md LICENSE BACKLOG.md ROADMAP.md CHANGELOG.md deploy/nomad/hlsdoctor.nomad.hcl deploy/streams.example.txt Dockerfile .dockerignore; do [ -f "$f" ] || fail "$f is missing"; done
grep -qi "read.only\|reads only" README.md || fail "README.md must state that hlsdoctor is read-only"
grep -qi "never prints\|never printed\|never in the output" README.md || fail "README.md must state what is never printed (query strings, headers)"
grep -q "never sends" README.md || fail "README.md must state that RTMP stops at the handshake (never sends connect, publish or play)"
grep -q "hlsdoctor" deploy/nomad/hlsdoctor.nomad.hcl || fail "the Nomad job spec must run hlsdoctor"
grep -q 'type *= *"batch"' deploy/nomad/hlsdoctor.nomad.hcl || fail "the Nomad job must be a periodic batch job"
# The image stamps the same version variable the release binaries do, and never runs as root.
grep -q 'internal/version.Version=' Dockerfile || fail "the Dockerfile must set internal/version.Version from VERSION"
grep -q '^USER nonroot' Dockerfile || fail "the image must run as nonroot"
# Every finding code the package documents appears in the README's table.
for code in $(grep -oE '^//	[a-z-]+ ' internal/findings/findings.go | awk '{print $2}' | sort -u); do
  grep -q "\`$code\`" README.md || fail "finding code $code is not in the README table"
done
echo "ok — repo invariants hold at $V"
