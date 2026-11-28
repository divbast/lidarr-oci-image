<!-- SPDX-License-Identifier: GPL-3.0-only -->
<!-- SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee> -->

# lidarr-oci-image

Lidarr, the music collection manager, built from source in a container
image that runs at zero Linux capabilities, with no new privileges, on a
read-only root filesystem as a non-root user. It follows Lidarr 4's
nightly line, upstream's develop branch on .NET 10, which upstream
publishes only as nightly builds, until upstream publishes v4 stable, and
then moves to stable by hand ([Moving to stable](#moving-to-stable)). It
was made from oci-image-template with the template's .NET layer, and its
Servarr parts follow sonarr-oci-image's. The divbast organisation's
settings, rulesets and Renovate wiring live in `divbast/infra`.

It publishes to `ghcr.io/divbast/lidarr`, tagged with Lidarr's version
and `latest`, from `build.yml`'s daily slot and its manual run on `main`.

What makes the image this app's lives here: the Containerfile, the health
probe (`cmd/divbast-lidarr`, `internal/probe`), the backend stage's two
checks (`test/global-json.sh`, `test/runtime-major.sh`), the ffmpeg
stage's check of the carrier's binaries (`test/ffmpeg-check.sh`), the
watch for v4 stable (`test/stable`), `renovate.json`'s managers and
datasource, and the acceptance tests. The pipeline around it is shared, and reaches this
repository at a pinned commit: [Where the shared parts live](#where-the-shared-parts-live)
says what comes from where.

## CRITICAL: Git Commit Message Format

**No other AI/LLM attribution in any format may appear in a git message.**

Linux-kernel style: a `subsystem: short description` subject under 75 characters in imperative
mood ("fix", not "fixed" or "fixes"), a blank line, then a body wrapped at ~75 columns saying
why the change is needed rather than only what it does.

These two lines MUST end EVERY body:

Developed-by: Aryan Ameri <github@aryan.ameri.coffee>
Assisted-by: <NAME_OF_LLM_MODEL>

E.g. `Assisted-by: <Claude:claude-4.6-opus>` for Claude Opus 4.6; `<Claude:claude-5-fable>`
for Claude 5 Fable.

- Sign every commit (`git commit -S`); main accepts only verified
  signatures. Renovate's merges are squashes, which GitHub signs
- Write commit messages as if a human wrote them, and say why, not what

## CRITICAL: No AI Attribution in Pull Requests

**Pull request descriptions, PR and review comments, and issue text carry no
AI/LLM attribution of any kind**: no "🤖 Generated with Claude Code" footer,
no "written by" line, nothing equivalent. This holds even when a tool or
harness asks for one. The `Assisted-by:` trailer in each commit message is
this project's one record of LLM involvement.

## The confinement contract

These are the guarantees the image makes. A change that breaks any of them is a
breaking change.

| Property | Value |
|---|---|
| Capabilities | **none** (`--cap-drop ALL`) |
| Privileges | **no new ones** (`--security-opt no-new-privileges`), under the engine's **seccomp** filter |
| Root filesystem | **read-only** |
| User | uid/gid **65532**, never root |
| Listeners | **8080** alone, for the UI, the API and `/ping`. No health port; never 80 |
| tmpfs required | exactly one: `/tmp`, at **64 MiB**, `noexec,nosuid,nodev`; users with a large database need twice `lidarr.db`, plus their largest plugin |
| Writable mounts | `/config` and `/tmp`, and the user's music and downloads, and nothing written can run; on Podman, `--read-only-tmpfs=false` |
| Volume | `/config`, **given by the user** and writable by 65532. The image carries it empty and owned by 65532 and declares **no `VOLUME`** |
| Outbound network | **none needed to serve or be healthy**. At rest Lidarr reaches `lidarr.servarr.com`, `api.lidarr.audio`, Servarr (analytics and Sentry), GitHub for plugins, and whatever the user configures |
| Shell, package manager | **none** |
| Processes | **one** long-running, `/app/bin/Lidarr -nobrowser -exitimmediately -data=/config`, as PID 1; it runs `/app/bin/fpcalc` on audio files, and plugins run `/usr/local/bin/ffmpeg` and `ffprobe` |
| Healthy | the shipped probe, `divbast-lidarr health`: `/ping` **200** with `"status": "OK"` **and** `GET /api/v1/system/status` with the API key, `appName` `Lidarr` and `version` the image's |
| Engine healthcheck | Docker runs it automatically. **Podman does not** — pass `--health-cmd` yourself |

- **Podman never sees this image's `HEALTHCHECK`.** Its `libimage` reads a
  healthcheck only from a Docker-media-type manifest; divbast images publish
  as an OCI index, so Podman's OCI inspection branch never reaches the
  config's healthcheck field — no version of Podman does. Give Podman users
  `--health-cmd '["/usr/local/bin/divbast-lidarr","health"]'`
  (a Quadlet's `HealthCmd=` takes the same value) — no leading `"CMD"`,
  because Podman before 5.8.0 re-splits such an array into one token instead
  of running it. Tracked upstream as podman/podman#25454 and #18904. The
  contract suite checks the image's `HEALTHCHECK` is that array in exec
  form, and has each engine judge the container
- **The probe asserts two halves, and neither implies the other.** `/ping`
  runs a database query and shows the server answers; the status, asked
  with the API key, which every authentication method accepts, shows the
  build is the pinned one. It reads the key from `LIDARR__AUTH__APIKEY`,
  else `/config/config.xml`, and the URL base from
  `LIDARR__SERVER__URLBASE`, else config.xml's `<UrlBase>`, since a base
  set in the UI lives only there. It never follows a redirect: Lidarr
  redirects a request lacking its base, which is the misconfiguration the
  probe exists to show. While Lidarr starts, `/ping` answers 503 with
  `Lidarr is starting up`, which the probe reports. It prints nothing
  when healthy; each failure is a line, `ping: <reason>` or `status:
  <reason>`, since with no shell the engine's health log is the only
  record of why
- **The HEALTHCHECK's `--timeout` must clear the probe's own budget.** The
  probe allows each half 4 s, one after the other, once it has waited up
  to 160 ms to re-read a config.xml caught mid-write: 8 s plus 160 ms at
  worst, still under the `HEALTHCHECK`'s 10 s, which lets the probe print
  its own line instead of an engine timeout. The `--start-period` is
  300 s: Lidarr listens only once its migrations finish, a nightly may add
  one a large library takes a while over, and a container marked unhealthy
  mid-migration may be killed by autoheal or Swarm. The first passing
  check ends the start period, so a healthy start loses nothing. The
  contract's `Healthcheck` must match it
- **Nothing listens but 8080.** The contract suite reads the kernel's socket
  tables and fails on any other TCP port a process in the container
  listens on, and on any UDP socket one holds. `DOTNET_EnableDiagnostics=0`
  keeps the runtime from opening its diagnostics socket
- **One process, always, and it stops when told.** Lidarr is the
  ENTRYPOINT, so it is PID 1, and .NET handles SIGTERM there and reaps its
  children, `fpcalc` and a plugin's `ffmpeg` among them, so it needs no
  init. A restart from the UI, which a plugin install asks for, happens
  inside the process, which `TestRestartsInProcess` holds. Lidarr stopped
  in 0.21 to 0.26 s alone, and within the suite in 0.25 to 0.72 s under
  Docker and 0.35 to 1.18 s under Podman; `TestStopsPromptly` fails on a stop that takes longer than 4.3 s,
  Prowlarr's and Sonarr's budget, or any exit but 0. Do not add a
  supervisor or a second long-running process
- **`-exitimmediately` stays.** A startup failure upstream calls
  non-recoverable, a corrupt `config.xml` among them, then exits 3 at once.
  Without it Lidarr waits an hour for a key press no one can make in a
  container, and the container sits there looking alive.
  `TestExitsOnCorruptConfig` holds it
- **`/config` fails visibly, though not as Sonarr's does.** Lidarr's first
  write is its error reporter's folder, `/config/Sentry`, before its own
  check of `/config`. A `/config` 65532 cannot write makes it exit 1 within
  two seconds, logging `Access to the path '/config/Sentry' is denied`
  (`TestRefusesUnwritableConfig`). With no mount at all it dies within a
  second, logging `Read-only file system : '/config/Sentry'`, with the
  runtime's signal as its status: 133 on arm64, 139 measured on amd64
  under emulation, so `TestRefusesNoConfigMount` asks only for a non-zero
  exit and that line
- **Some startup failures exit 0, and that is upstream's.** A failure
  inside the web host, such as a PID file it cannot write (measured), a
  port in use or a corrupt database, logs `Lidarr failed to start: …` and
  exits normally, so an `on-failure` restart policy never fires. The README
  says so. Nothing here can change it without patching upstream
- **`/tmp` is the size a backup, a restore and a plugin need.** Lidarr
  copies the database into `/tmp` before it zips the backup, receives a
  restore's upload there, and downloads a plugin's zip there before it
  installs it, so `/tmp` must hold twice `lidarr.db` and the largest
  plugin, or backups, the weekly scheduled one included, fail. A backup of
  a 614,400-byte database peaked at 608 KiB, failed on a 512 KiB `/tmp`
  and fitted 1 MiB, as did its restore; the suite's 64 MiB is far above
  that, and `TestBackupFitsTmpfs` and `TestRestoresBackup` hold it; the
  README tells users how to size theirs
- **A restore needs a restart of the container.** The restore's upload
  stages the database, and Lidarr's in-process restart puts it in place
  (`Restoring Database`), but upstream clears SQLite's connection pool
  only on a full shutdown (`Bootstrap.Start`, and after a backup), never
  between in-process restarts, so the new host can reuse a connection to
  the replaced, unlinked `lidarr.db` and read the old data. It never
  showed on a Mac and showed on GitHub's arm64 runners, under both
  engines. A restore that fails partway, as Sonarr's can when a closing
  connection takes `-wal` away, does not stop Lidarr at once, as it stops
  Sonarr with exit status 3: `RunHost` catches any failure to start a
  host and logs `Error starting with plugins enabled`, and if its plugins
  unload (`PluginLoader.UnloadPlugins`, which gives up after ten rounds
  of garbage collection), Lidarr builds the host again without them,
  which runs the still-staged restore again. If they do not unload, or
  the second try fails too, Lidarr ends, with `Lidarr has shut down
  completely` and exit status 0, the restore still staged (measured, two
  failures in a row, with `lidarr.db-wal` a directory the restore cannot
  delete). Lidarr's restore deletes the old files, allowing for one
  already gone, where Sonarr's moves each aside after a check for it, so
  Lidarr usually gets past a failed restore by itself. `TestRestoresBackup`
  still restarts the container once Lidarr logs that it is restoring,
  whether the container still runs or has stopped, logging which way the
  in-process restart went, before it looks for the restored tag, and the
  README tells users to do the same, before changing anything. Upstream's
  own fix would be a `ClearAllPools` in its restart loop; this image does
  not patch it
- **No egress is needed, and some happens.** The contract suite proves the
  image serves and is healthy on a genuinely `--internal` network. The
  acceptance suite points Lidarr's own hosts at loopback and turns
  analytics off in every container but the contract's own, which runs as
  shipped
- **Analytics stay on**, as upstream ships them (the owner's decision). The
  image sets nothing for them; the README says how to turn them off
- **Authentication stays as upstream ships it**: off on a fresh install,
  method `None`, no first user and no first password. Until a method is
  set, the UI, `/initialize.json`, which holds the API key, and backup
  downloads answer anyone; after Forms, each sends a visitor with no
  session to the login page. The README says to choose Forms at the first
  visit. The suite makes the first user as the UI does, with `GET` then
  `PUT /api/v1/config/host` (202), and `TestFormsSignIn` signs in through
  the form. Environment values are case-sensitive:
  `LIDARR__AUTH__METHOD=forms` is ignored without a word, and
  authentication stays off; the README says to write `Forms`
- **The updater is off.** `/app/package_info` says `UpdateMethod=Docker`
  and `Branch=nightly`, and upstream reads it only from beside a directory
  named `bin`, which is why the binaries are in `/app/bin`.
  `Lidarr.Update` is not shipped. `TestUpdaterIsDocker` and
  `TestMigratedConfig` hold it, the latter for a linuxserver or hotio
  `config.xml` that says `master`, which Lidarr then logs it changes to
  `nightly`
- **Plugins live in `/config/plugins`,** since `/app` is read-only. A fresh
  install has no such folder; an install makes it, through `/tmp/plugins`,
  and an in-process restart loads it. `TestRestartsInProcess` holds that a
  restart keeps the folder, and `TestPluginsEmpty` that the plugin list
  answers on a fresh install. Only plugins that publish a `net10.0` asset
  install on this .NET 10 build. Plugins are third-party code with
  Lidarr's access; no test installs one, which would need GitHub and code
  no one has reviewed
- **Nothing may add a shell or a package manager to the final image.** The
  base is DHI's `aspnetcore`; that is the point, and the contract's
  `no-shell` and `no-package-manager` checks hold it there. That means no
  Custom Script connection, Import Using Script, Script updater or any
  interpreted script can work, and the README says so. What the base
  removes is us running apt, not the patching itself: ICU, OpenSSL, glibc
  and the .NET runtime are all executable code that takes CVEs, and
  **nothing in this repository collects their fixes on its own.**
  Rebuilding an unchanged Containerfile does not pick them up. **Renovate
  is the update path**: it runs every morning, after every merge to main
  and whenever CI finishes on one of its branches, from
  `.github/workflows/renovate.yml`, which calls divbast/ci's
  `renovate.yml`, as the org's `divbast-renovate` App, managed from
  `divbast/infra`. It moves the DHI images by digest, with the .NET SDK and
  runtime grouped in one PR and .NET majors held for Dashboard approval
  (renovate-config's `:app`), Lidarr's nightlies as they come, and the
  digest of the ffmpeg carrier, `ghcr.io/divbast/ffmpeg:lidarr`, which
  divbast/native-tools publishes, with no release-age hold, as divbast's
  own packages have none. After a merge
  it rebases every open PR left behind main, since GitHub re-runs no
  checks when a base moves and a green check on a stale base proves
  nothing. **It merges its own PRs** once every check passes, with a
  squash, while the repository variable `LIDARR_AUTOMERGE` is `enabled`;
  any other value stops merging from the next run. A new major of
  divbast/ci, and a raise of `go.mod`'s `go` line, wait for approval on the
  Dependency Dashboard first, and the stable watch's PR never merges
  itself. **A merge never publishes.** `build.yml`'s daily run is the
  publishing slot, through divbast/ci's `app-publish.yml`, and a manual run
  of `build` publishes at once. `.github/workflows/stall.yml`, through
  divbast/ci's `stall.yml`, fails each day that updates have stopped
  flowing: a Renovate PR stuck, Renovate silent, a dependency Renovate
  could not look up, Azure's build list among them, or a daily slot that
  should have published and did not
- **The publish job publishes for real.** `build.yml` calls
  `app-publish.yml` with `image: ghcr.io/divbast/lidarr` and no
  `rehearse`, an input divbast/ci permits only in the template. It tags the
  attested index with `LIDARR_VERSION` and `latest`. `stall.yml` passes
  `published: ghcr.io/divbast/lidarr:latest`, so its slots check is on: it
  counts the daily slots that should have published and did not
- **Every image the Containerfile builds from is verified first.** Before
  `app-check.yml` builds anything, `divbast-verify-images` refuses any image
  not pinned as `<name>:<tag>@sha256:<digest>`, and verifies Docker's
  signature on each DHI image, the runtime and **every stage's image**:
  the fetch and frontend stages' `dhi.io/node` `-dev`, the backend's
  `dhi.io/dotnet` SDK and the probe and ffmpeg stages' `dhi.io/golang`
  `-dev`. Every stage is DHI, so one publisher signs every builder. The
  ffmpeg carrier is not DHI: the same tool checks its attestation, and its
  digest is the one the ffmpeg and sbom stages copy from. A local
  `just build` does not verify, and needs `docker login dhi.io`; `just
  ci-tools && .ci/bin/divbast-verify-images` verifies, with cosign from
  `mise.toml`
- **No binary may carry a file capability.** A binary with a file
  capability cannot be exec'd at all under `--cap-drop ALL` — it dies with
  `Operation not permitted` and exit 126 — and the contract suite's
  `file-capability` check, which runs `divbast-lidarr version`, fails on
  one, as `no-file-capability-xattr` fails on one anywhere in the image.
  `TestFpcalcRuns` runs `fpcalc`, and `TestFFmpegTranscodes` runs `ffmpeg`
  and `ffprobe` from `PATH`, as Lidarr's user under the contract
- **The application is root-owned and never written.** `/app`,
  `/usr/local/bin` and `/usr/local/share/doc/ffmpeg` are `COPY`'d from
  the build stages. Lidarr writes to
  `/config`, `TMPDIR` (`/tmp`) and the user's music and downloads alone

### The rules the build keeps

- **Never patch upstream.** The one change to an upstream file is the
  backend stage's rewrite of `global.json` by `test/global-json.sh`, which
  keeps upstream's SDK version as the floor and adds `"rollForward":
  "latestFeature"`, so a later SDK band, which Renovate moves the image to,
  can build; it never ships. A different change to Lidarr's source is a
  fork, which the design ruled out
- **Two build-stage checks guard a .NET major**, which would otherwise
  show only as dotnet's own error, or as an app that will not start.
  `test/global-json.sh` holds the SDK `global.json` names to the image's
  major and minor before it rewrites the file; `test/runtime-major.sh`
  holds the runtimeconfigs copied to `/out/bin` to the runtime image's
  tag, which the backend stage reads through a bare `ARG RUNTIME_IMAGE`,
  alone on its line, since Renovate would read anything after it as the
  runtime pin, and declared just above the check, since every later RUN
  has an ARG's value in its cache key and a new runtime pin would
  otherwise rebuild the restore and publish. Each fails a mismatch with
  the steps to take, naming [A .NET major](#a-net-major). Each is a
  build-stage check under "Code quality"'s rules, and its Go test,
  `test/globaljson` or `test/runtimemajor`, also checks the Containerfile
  runs it where it must. The two scripts and their tests are
  oci-image-template's .NET layer's, prowlarr-oci-image's and
  sonarr-oci-image's too, byte for byte but for the SPDX line, so a change
  to one goes to all four
- **The version and the branch are always passed.**
  `-p:AssemblyVersion=${LIDARR_VERSION}` and
  `-p:AssemblyConfiguration=develop` override upstream's `10.0.0.*` and
  `Release-dev`, under which Lidarr runs as a development build, with
  analytics and Sentry forced on whatever the user sets, the API
  reporting 10.0.0.x, and every plugin's minimum-version check passed.
  They are `-p:` properties, so no file is edited. The probe compares the
  status's version with the same value, through `-X main.version`.
  `develop` in the assembly is the branch upstream's own CI stamps a
  nightly with; `nightly` in `package_info` is the update channel the
  image follows, which Lidarr reports and its update check asks about.
  Both stay until the image moves to stable, when both become `master`
- **NuGet: no lock files, signatures verified, advisories warned of.**
  Upstream keeps no `packages.lock.json`, pins every package by an exact
  version from nuget.org alone, whose versions are immutable and
  repository-signed, and NuGet restores the lowest version a range allows.
  Ours would need regenerating by a person at every upstream bump. So the
  backend stage sets `DOTNET_NUGET_SIGNATURE_VERIFICATION=true`, and
  publishes under `--network=none` once restored. Upstream treats every
  warning as an error, NuGet's audit among them, so a new advisory against
  a package an already-pinned commit restores would fail the build, and
  hold back every other update, the runtime's patches included, until
  upstream moved: `-p:WarningsNotAsErrors=NU1901%3BNU1902%3BNU1903%3BNU1904`
  keeps those four warnings, and ci's scan blocks a vulnerable package the
  image does not already ship. The msbuild switch `-warnNotAsError` does
  not reach NuGet; only the property does. Proved for Lidarr: with one of
  upstream's six `NuGetAuditSuppress` lines deleted, the restore fails with
  `error NU1902`, and the property turns it back into a warning
- **The frontend runs no install script.** `yarn install
  --frozen-lockfile --ignore-scripts`, then `yarn run build --env
  production`, upstream's webpack build: Lidarr's own package.json has no
  install script, the build needs none, and an install script is how most
  malicious npm releases have run. A dependency's script the build cannot
  do without would fail it, but an install script upstream adds to its own
  package.json would be skipped without a word, and the UI built without
  whatever it did: read a Lidarr bump's package.json changes for one. The
  fix is to name why in the Containerfile, not to drop the flag quietly
- **fpcalc gets its execute bit back.** NuGet extracts it without one; the
  backend stage `chmod`s it and asserts it is executable, and
  `TestFpcalcRuns` runs it and finds no fpcalc entry on the Health page,
  which is where a missing bit shows: Lidarr calls it too old, not missing
- **No tag check on a nightly.** A nightly has no tag, so the fetch stage
  checks out `LIDARR_COMMIT` by its full hash and asserts nothing more.
  The version beside it is Azure's record of that build, carried by
  Renovate, which writes both lines in one change. An ancestry check
  against develop would turn the build red, and hold back the base's
  patches, each time upstream rewrites develop
- **Every stage is DHI.** The fetch runs in the Node `-dev` image, which
  ships git, the probe and ffmpeg stages in the Go `-dev` image, and the
  frontend and the sbom stage in the same Node image, with yarn 1 and
  `--frozen-lockfile`. The one image not DHI's is the ffmpeg carrier,
  which the stages only copy from. Never add a stage from another
  publisher, and never set
  `DOTNET_SYSTEM_GLOBALIZATION_INVARIANT`: it breaks name matching and
  release parsing
- **ffmpeg is not built here.** FFmpeg is built once, narrow and hardened,
  in divbast/native-tools, with the static LAME, Ogg, Opus and Vorbis
  libraries, and attested there: see that repository's README, "Using a
  carrier". A change to which formats it reads or writes, to its flags or
  to its codec libraries is a change there, and widening it is the owner's
  call. The `ffmpeg` stage copies `/bin/ffmpeg` and `/bin/ffprobe` from
  the `ghcr.io/divbast/ffmpeg:lidarr` carrier, by digest, and runs
  `test/ffmpeg-check.sh` over them as its last step: each answers as
  itself, is position-independent and bound at load, ffmpeg has the three
  library encoders, and neither needs a shared library but libc, libm,
  libz and the loader. The final stage copies the two binaries from that
  stage, so what passed is what ships, and a new carrier digest passes the
  same check before it merges. `test/ffmpeg` runs the script over fixtures
  and checks the Containerfile wires it that way: the carrier's binaries
  copied in, the check last, the final stage copying from the stage.
  `divbast-verify-images` checks the digest's attestation. FFmpeg's licence
  and the copyright files of the libraries linked in come from the
  carrier's `/doc` into `/usr/local/share/doc/ffmpeg`, which
  `TestFFmpegNotices` holds; the README has no licence section.
  `TestFFmpegTranscodes` encodes to and decodes back from MP3, Opus,
  Vorbis and AAC. A static FFmpeg carries no package record, so the
  `sbom` stage takes the carrier's `/records/sbom.cdx.json` as written,
  FFmpeg by its CPE and the four codec `-dev` packages by DHI package URL,
  adds only the path of ffmpeg, and fails if a component has neither a
  CPE nor a package URL or the five names change. The result is
  `/usr/local/share/divbast/sbom.cdx.json`, which the
  `io.github.divbast.scan.sboms` label declares and divbast/ci's scan
  (v4.30.0 or newer) reads
- **fpcalc carries an FFmpeg of its own,** statically linked into
  upstream's NuGet pin of Chromaprint (libavcodec 63.1 at this pin), which
  the image neither narrows nor hardens and the scan cannot see. Lidarr
  runs it on every file it imports. It moves only when upstream moves the
  package; the README says so, and that Allow Fingerprinting set to Never
  stops Lidarr running it
- **The probe is stdlib Go.** `cmd/divbast-lidarr` and `internal/probe`
  import nothing outside the standard library, since the build context
  leaves `.ci/` out, and divbast/ci's `probekit` would put ci code in the
  image
- **`LIDARR_VERSION` and `LIDARR_COMMIT` stay adjacent, in that order.**
  `renovate.json`'s
  nightly manager moves the first two in one PR from the custom datasource
  `lidarr-nightly`: Azure DevOps' public build list, CI builds of
  `refs/heads/develop` that succeeded, never a pull request's build, whose
  numbers share the counter. Its transform builds `releases` in brackets,
  since JSONata collapses a one-element list. If upstream ever moves its
  builds off Azure, the list stops growing and nothing fails: the image
  stays on its last nightly with no red check, which the README says.
  The carrier's digest needs no manager here: Renovate's dockerfile
  manager reads the `COPY --from=ghcr.io/divbast/ffmpeg:lidarr@sha256:…`
  lines and the `ffmpeg` and `sbom` stages' copies hold the one digest
- **The stable watch stays, until the move to stable.** The
  Containerfile's `# LIDARR_STABLE_V4=0.0.0.0` line is `renovate.json`'s
  watch manager's: Renovate writes Lidarr's first stable v4 release there,
  offered only releases GitHub calls stable and of v4 or later, never its
  tags, which also hold Sonarr's and Radarr's, and `TestNoV4StableYet`
  (`test/stable`) fails once it moves, and if the line is lost or
  doubled. It lives in the Containerfile because the recommended preset
  ignores every path under `test/`. Its floor, 0.0.0.0, shares no major,
  minor and patch with the `4.0.0.N` nightlies, since Renovate offers a
  pre-release that shares all three with the current version. The nightly
  and the watch share the group `Lidarr`, with `separateMajorMinor` off so
  the watch's first, major, update stays in the group's branch; a branch
  holding the watch's update is not automerged, and its check holds it
  red. So once v4 stable exists, the nightly pins stop moving. FFmpeg and
  its libraries stay outside the group, so the freeze never holds them.
  Re-prove every manager with a local Renovate run before changing one

## A .NET major

A .NET major moves by hand, about once per LTS, because neither of its two
pull requests can go green alone: Lidarr's retargeted nightly needs the
new SDK, and the SDK and runtime the Dashboard holds back cannot build or
run the nightly before it. The backend stage's two checks fail each with a
message naming this section; only were Lidarr to drop its `global.json`
and retarget first would dotnet's own error come instead. Each step needs
the user's go-ahead:

1. Lidarr's Renovate PR, the group `Lidarr`, which moves `LIDARR_VERSION`
   and `LIDARR_COMMIT`, is red with one of the checks' messages, where `W`
   is the new .NET:
   `ERROR: global.json asks for the .NET W SDK; … Approve the .NET W major …`
   or `ERROR: the app targets .NET W; … Approve …`.
2. Approve the major, the "dhi .NET SDK and runtime" group, on the
   Dependency Dashboard. Renovate opens its PR, red on its own: a check
   fails it with `ERROR: … A .NET major waits until … retargets …`, which
   here is expected, and no reason to stop.
3. On a branch from main, take both Renovate commits with
   `git cherry-pick -S`, open a PR, and merge it when green. Nothing the
   Containerfile runs names the target framework, so those two commits are
   the whole change: the backend stage finds the one folder matching
   `_output/net*/<rid>/publish`, and refuses none or several.
4. Close the two Renovate PRs.

A .NET major also changes the plugin asset name Lidarr looks for
(`net<major>.0`), so plugins installed under the old one stop installing
until their authors publish for the new: say so in the PR.

## Moving to stable

The image follows Lidarr 4's nightlies until upstream publishes v4
stable, and then moves to stable by hand, once. Nobody has to be watching
for it: Renovate notices, and stops the nightly line where it is. Each
step needs the user's go-ahead:

1. The group `Lidarr`'s Renovate PR is red: it moves the Containerfile's
   `# LIDARR_STABLE_V4=` line to Lidarr's first stable v4 release, and
   `TestNoV4StableYet` fails it with `Lidarr v4 is stable: release <it>.
   The nightly line stops here until the image moves to stable: CLAUDE.md,
   'Moving to stable'.` Every nightly after it lands in that same PR, so
   `main` stays on the nightly it has, and stall's `stuck` check goes red
   within a day.
2. Pick the stable release to move to: the first that carries every
   database migration the pinned nightly has, since a Lidarr older than the
   database it opens meets migrations it does not know. Each file under
   `src/NzbDrone.Core/Datastore/Migration/` at `LIDARR_COMMIT` must be at
   the stable release's commit, by number and name. Nightlies run ahead of
   upstream's promotions, so the pinned commit is usually newer than the
   first stable's, and `git merge-base --is-ancestor` fails: compare the
   files. If the pinned nightly has a migration the stable lacks, wait for
   the next stable.
3. On a branch from main, in one PR:
   - `renovate.json`: the nightly manager follows Lidarr's GitHub
     releases, stable ones alone, as prowlarr-oci-image's manager does
     (`github-releases`, the `v` added and taken off, the four-number
     versioning); delete `customDatasources.lidarr-nightly`, and the
     watch's manager and its rule; drop `Lidarr v4 stable` from the group
     rule's `matchDepNames`, and its `separateMajorMinor` and the
     description that explains them, keeping its `lidarr` label and
     `lidarr:` prefix; say so in the descriptions;
   - the Containerfile: `LIDARR_VERSION` and `LIDARR_COMMIT` to the release
     picked and the commit its tag names; the fetch stage asserts the
     release's tag still names the commit, as prowlarr-oci-image's does;
     delete the `# LIDARR_STABLE_V4=` line and its comment;
     `-p:AssemblyConfiguration=master` and `Branch=master`, upstream's
     names for stable builds; the comments that say nightly or develop;
   - delete `test/stable`;
   - `test/acceptance`: `nightlyBranch` becomes `master`. `TestMigratedConfig`
     then needs a `config.xml` on another branch for Lidarr to change:
     `migratedConfig`'s `<Branch>` becomes `nightly`, as a `config.xml`
     this image wrote before the move says, and `branchChanged` becomes
     `External updater uses branch master instead of the currently selected
     nightly, changing to master.`, the line Lidarr then logs;
   - `build.yml`'s `description`, this file, the README and divbast/.github's
     profile row: drop "nightly", the Azure source and its gap, and this
     section.

   Open the PR, and merge it when green.
4. Close Renovate's red PR if it is still open (Renovate usually closes
   it, or rebases it onto the new main, on its next run), and mark
   Lidarr's phase 7 done in the Servarr programme's records.

## Where the shared parts live

- **divbast/ci.** `just setup` checks it out into `.ci/`, which git and
  the build context both ignore, at the one commit every workflow calls it
  at: a release's full SHA, with its tag as the comment. Its README is the
  reference for all of it:
  - the reusable workflows: `app-check.yml` (lint; the image built for
    amd64 and arm64 once every image it builds from verifies; the acceptance
    suite on each under Docker and rootless Podman; the scan),
    `app-publish.yml` (the daily slot and the button only: push, index,
    attest, verify, then tag and check as an anonymous client pulls it),
    `renovate.yml` and `stall.yml`. `build.yml`, `renovate.yml` and
    `stall.yml` here call them, and keep their own triggers and the
    `required` job;
  - the recipes the justfile imports, `.ci/just/common.just` and
    `.ci/just/app.just`. Recipes here call them from a recipe's body, never
    as a dependency, so the justfile still parses before `.ci` exists;
  - the Go tools, which `just ci-tools` builds into `.ci/bin/`, run from
    this repository's root: `divbast-pin`, `divbast-verify-images`,
    `divbast-scan`, `divbast-testcount`, `divbast-published-revision`,
    `divbast-publish-decision`, `divbast-stall` and the rest;
  - the acceptance `harness` and the `contract` suite, which `go.mod`
    requires through `replace github.com/divbast/ci => ./.ci`;
  - `go/golangci.yml`, the one golangci-lint configuration, and
    `.github/actionlint.yaml`.

  A change to ci reaches this repository only through the Renovate PR that
  moves every pin of it at once. `just setup`, and the pin check
  (`divbast-pin`) in each ci workflow that runs this repository's code,
  refuse workflows that call ci at more than one commit
- **divbast/renovate-config** holds the presets `renovate.json` extends.
  `renovate.json` adds Lidarr's nightly datasource, its four managers and
  their rules, and nothing else
- **divbast/infra** holds the org's settings, this repository's rulesets,
  the `divbast-renovate` App, the `renovate` environment and
  `LIDARR_AUTOMERGE`

## Key commands

- `just setup` — fetch divbast/ci into `.ci/` at the commit the workflows
  pin, then install the pinned toolchain. One command on a fresh clone, and
  again whenever the pin moves; `just lint` fails, naming it, when `.ci` is
  not at the pinned commit (`ci-fresh`)
- `just build` — build the image locally, after `docker login dhi.io`. It
  does not verify the images it builds from; `just ci-tools &&
  .ci/bin/divbast-verify-images` verifies, with cosign from `mise.toml`
- `just acceptance` — `go test ./test/acceptance/...` against the image
  `just build` wrote, under `ENGINE` (docker unless it says podman),
  through `divbast-testcount`, which fails the run unless a test passed and
  none failed or skipped. `just IMAGE=… acceptance` tests another image.
  `ENGINE=docker IMAGE=localhost/lidarr:dev mise exec -- go test
  -run TestServes ./test/acceptance/` runs one test. A full run takes
  about 15 seconds under Docker and 45 under Podman
- `just test` — build, then the acceptance suite
- `just lint` — pinact, zizmor, actionlint, ShellCheck and shfmt, the Go
  gates (the compiler, `go vet`, `go fix -diff`, `go mod tidy -diff`,
  `golangci-lint fmt --diff` and `golangci-lint run`), `go-test`, `reuse
  lint`, hadolint and `docker build --check`. `go-test` runs every Go test
  under the race detector, `test/globaljson`, `test/runtimemajor`,
  `test/ffmpeg` and `test/stable` among them; the acceptance suite skips
  there, since `IMAGE` is unset
- `just archive`, `just scan` — write the image as an OCI archive, then run
  `divbast-scan` over it and the published `:latest`. Needs network access
  to ghcr.io
- `just actions-pin`, `just actions-update` — move the workflows' action pins
- `just ci` — every gate, locally. Run this before committing

## Where the toolchain comes from

- **`mise.toml` is where tool versions are written.** Go, golangci-lint,
  hadolint, ShellCheck, shfmt, actionlint, reuse, just, pinact, trivy,
  grype, zizmor and cosign are all listed there. Every justfile recipe but `setup`, which
  installs them, runs through `mise exec`, so a recipe resolves those
  binaries and not a same-named one on PATH. The recipes that call GitHub,
  pinact's and zizmor's, take a fine-grained token from `GITHUB_TOKEN` and
  nothing else, as divbast/ci's README says; CI passes its own
- **Go builds with the toolchain mise installs and nothing else.**
  `GOTOOLCHAIN=local` keeps `go.mod`'s line from downloading another, and
  `GOFLAGS=-mod=readonly` refuses to edit `go.mod`
- **`just lint` needs a running Docker daemon**, for `docker build --check`.
  `--check` is a buildx flag with no podman equivalent, so this one gate does
  not honour `ENGINE` and does not run without dockerd
- **`just scan` needs `skopeo` on `PATH`.** It reads the published image with
  [skopeo](https://github.com/containers/skopeo), which no `mise.toml` entry
  provides, so `just ci` needs it too. CI runners already have it
- **The acceptance suite under Docker needs `zstd` on `PATH`.** Every
  layer of a DHI image is zstd, and ci's harness reads the layers of the
  image Docker saves through the `zstd` command, since Go's standard
  library has no zstd reader yet; without it the contract's
  `no-file-capability-xattr` check fails, naming the layer. No
  `mise.toml` entry provides it. GitHub's runners have it, and a Mac gets
  it from Homebrew (`brew install zstd`). Podman saves its layers
  uncompressed, so a Podman run does not need it
- **`renovate.json` has a `.license` sidecar.** JSON has no comment syntax,
  and an SPDX tag written as a string value carries the closing quote into
  the expression, which `reuse` rejects. So does `tone.flac`, the
  transcode test's fixture, CC0-1.0, made by the image's own ffmpeg

## Code quality

- All linter gates are enforced as errors — fix them, don't suppress them. Any
  suppression, anywhere, needs an explicit, comment-level justification beside
  it to be accepted
- **The scan blocks only what a change would add, and never passes a
  report blind to the OS.** `divbast-scan` scans the candidate and the
  published `:latest` for the same arch from one database, and fails only
  on a HIGH or CRITICAL finding, the same vulnerability in the same
  package, that the published image lacks. No release can make an
  advisory against something already shipped any worse, so blocking on one
  only holds back whatever else the release fixes. Whether anything is
  published is the registry's to say, never the scanner's: the scan reads
  `:latest` as an anonymous client, and when ghcr.io refuses, asks GitHub
  why, with the check job's token, which is what its `packages: read` is
  for. Not published yet, or a package still private: every finding is
  reported, on the summary page, as a warning annotation and on stderr,
  and none blocks. Once the image is public, only new findings block. A
  public image the registry refuses, a registry that cannot be reached, or
  GitHub not answering fails the scan rather than waving it through. A
  local `just scan` never asks GitHub, so a refusal there is advisory,
  with a warning that only CI can tell what it means. **None of that
  softens the OS guard:** a report in which the scanner recognised no
  operating system, or saw no OS package, fails the scan, for the
  candidate and the published image alike and whether or not anything is
  published. The base says `ID="debian"` today, so Trivy scans it, and
  applies DHI's signed VEX statements for the runtime image, so Trivy's
  reading of DHI's `+dhiN` packages as plain Debian ones does not
  over-report; once DHI's base says `ID=dhi`, divbast/ci scans it with
  Grype, which reads DHI's own advisories. Neither sees the static
  FFmpeg, nor fpcalc and the FFmpeg inside it
- REUSE-compliant SPDX headers on every file; licence is **GPL-3.0-only**.
  The image's licences label names only that. Lidarr's own licence file
  ships in `/app/bin/LICENSE.md`, where upstream's tarball puts it, and the
  label and the README say nothing more of it
- **The acceptance suite is Go, on divbast/ci's harness.** Every
  container that runs Lidarr runs under `harness.Contract("64m")`, the
  *exact* confinement the contract claims, `--security-opt
  no-new-privileges` and `--tmpfs /tmp:size=64m,noexec,nosuid,nodev`
  included, so a check can never pass under looser settings than we ship.
  What CI proves is what ships. The one exception, `asRoot`, runs only
  the suite's own probe (`test/probe`), to leave a volume as root would.
  `TestContract` runs ci's contract suite against the image; the rest are
  Lidarr's own, one topic per file, as the README's "The pipeline" lists
  them. Each serves through the harness's `Serve`, which publishes on the
  host's loopback at ports the engine picks, so no two tests, or two
  suites on one host, ask for the same one. The assertions (`StatusIs`,
  `Answers`, `Refuses` and the rest) are the harness's too, shared with
  every divbast image, and every wait is `harness.Poll`. Each thing the
  image serves or guarantees has a test. Under Podman every container gets
  `--log-driver k8s-file`: Podman's default under systemd, journald, drops
  a unit's messages past its rate limit, and a run starting twenty
  containers at once can lose the very lines its tests wait for
- **Go is modern and idiomatic, and linted strictly**, under ci's
  `go/golangci.yml`. Bash is for what is short and will stay so: the
  justfile's recipes. A check that runs inside a Containerfile build stage
  is the exception, since a build stage has bash and no Go: it stays bash,
  under ShellCheck `enable=all`, shfmt and `set -euo pipefail`, with a Go
  test that runs it against fixtures and sees each way it can refuse
- A test suite that has never failed proves nothing. When adding a check, prove
  it can fail before trusting it
