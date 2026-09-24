# Contributing

## Local loop

```bash
go test ./... -count=1                  # unit tests + the CLI against a local origin and a fake RTMP server
gofmt -l cmd internal && go vet ./...
./scripts/check-repo.sh                 # VERSION ↔ CHANGELOG, README statements, the job spec, the findings table
go run ./cmd/hlsdoctor check https://demo.unified-streaming.com/k8s/live/stable/scte35.isml/.m3u8
npm install && npm run backlog && npm run build:site   # tooling only: backlog lint, site from README
```

No stream needed to develop: the tests start an `httptest` origin that serves the
fixtures in `testdata/` (one variant advancing, one stale, one gone) and a TCP server
that answers the RTMP handshake.

## Adding a finding, a check or a parser tag

1. Confirm the behaviour on the specification (RFC 8216, the HLS 2nd edition draft for
   low latency, the Adobe RTMP specification) or on a real stream; date the fact in
   `CLAUDE.md`.
2. Extend the fixtures (`testdata/`) or the test origin and write the assertion first.
3. Add the code to the doc comment of `findings.Evaluate` **and** to the README table —
   `scripts/check-repo.sh` fails when they differ.
4. Keep rule 2: nothing that could carry a token reaches the output.

## Verifying on the farm

```bash
hlsdoctor ls https://<cdn>/live/<channel>/master.m3u8
hlsdoctor check --from streams.txt --exit-on bad --json | jq .worst
hlsdoctor check https://<cdn>/live/<channel>/master.m3u8 --each-ip   # every origin behind the name
```

Record a week of `check` runs against the channels — findings that were true, findings
that were noise, streams that broke without a finding — in the README, dated, before
tagging 0.1.0.

## Backlog, roadmap, issues

`BACKLOG.md` is the single source of truth; `ROADMAP.md` is generated from it and the
GitHub issues are synced from it one way on every push to `main` that touches the file.
Items carry a stable `HLD-n` id and `<!-- hld: prio= size= labels= [ver=] -->`.

## Pull requests

`main` is protected: pull request, green CI, no direct pushes. Conventional subject with
the `HLD-n` id, a CHANGELOG line under `[Unreleased]`.

## Releasing

```bash
# bump VERSION; rename CHANGELOG's [Unreleased] to [x.y.z] — date and open a new [Unreleased];
# ver=main → ver=x.y.z in BACKLOG.md; regenerate the roadmap; land it by pull request
git checkout main && git pull
git tag v$(cat VERSION) && git push origin v$(cat VERSION)
```

`release.yml` verifies the tag against `VERSION`, runs the tests, builds static binaries
for linux/amd64, linux/arm64 and darwin/arm64 with the version baked in, attaches them
with a checksums file to the GitHub release (notes from the CHANGELOG section) and
closes the milestone whose title starts with `v<version>`. `release-drift.yml` fails
when `main` carries a VERSION with no tag for two hours. Re-run with
`gh workflow run Release -f tag=v<version>`.
