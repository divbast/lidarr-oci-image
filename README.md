<!-- SPDX-License-Identifier: GPL-3.0-only -->
<!-- SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee> -->

# lidarr-oci-image

[Lidarr](https://github.com/Lidarr/Lidarr), the music collection manager,
in an image that runs at zero Linux capabilities, with no new privileges,
on a read-only root filesystem as a non-root user, with no shell in it. It
is built from Lidarr's source, and goes through divbast/ci's shared
pipeline: lint, a build for amd64 and arm64, the confinement contract and
its own acceptance suite under Docker and rootless Podman, a vulnerability
scan, and a daily publish to `ghcr.io/divbast/lidarr`.
[Publishing](#publishing) says when it publishes and under which tags.

**This is Lidarr 4, upstream's nightly line.** Lidarr's stable releases,
3.1, and its 3.2 pre-releases run on .NET 8, out of support since
November 2026. Upstream's develop branch moved to .NET 10 as Lidarr 4.0.0,
which it publishes only as nightly builds, so the image follows those
until upstream publishes v4 stable, and then moves to stable. [The nightly
line](#the-nightly-line) says what that means for you: nothing to do but
expect a nightly's changes, and keep backups.

## Base

The image is a Lidarr nightly, built from source: the version and commit
the Containerfile's `LIDARR_VERSION` and `LIDARR_COMMIT` name, which are
the build number upstream's CI gave that commit and the commit itself. It
runs on Docker Hardened Images'
[`aspnetcore`](https://hub.docker.com/hardened-images/catalog) for Debian
13: the .NET runtime, ICU and OpenSSL, no shell and no package manager.
The app is built framework-dependent, so rebuilding the image patches the
runtime. Every stage of the build is also a DHI image, and CI refuses to
build until Docker's signature on each is verified:

- `fetch` checks out the commit by its full hash. A nightly has no tag to
  check it against: the hash fixes the content.
- `frontend` builds the UI with yarn and webpack, from `yarn.lock`, which
  pins every package by its hash, and runs no package's install script.
- `backend` publishes the .NET app, with NuGet checking the signature of
  each package. Upstream keeps no lock file and pins every package by an
  exact version from nuget.org alone, so there is none here. NuGet's
  advisories stay warnings in the build log, where upstream makes them
  errors: the scan already blocks a vulnerable package the image does not
  ship yet, and an error would hold back every other update until upstream
  moved.
- `probe` builds the health probe, a static Go binary on the standard
  library alone.
- `ffmpeg` builds ffmpeg and ffprobe from FFmpeg's source at a release
  tag, for Lidarr's plugins: [ffmpeg and ffprobe](#ffmpeg-and-ffprobe).

Lidarr is never patched. The one change the build makes to an upstream
file is to `global.json`, in the backend stage, so that any later .NET SDK
of the same major and minor can build it; that file does not ship.

## Container Features

| Property | Value |
|---|---|
| Capabilities | none (`--cap-drop ALL`) |
| Privileges | no new ones (`--security-opt no-new-privileges`), under the engine's seccomp filter |
| Root filesystem | read-only |
| User | uid/gid 65532 (`nonroot`) |
| Port | 8080 (the UI, the API and `/ping`), and nothing else listens |
| tmpfs required | `/tmp`, which nothing can run from (`--tmpfs /tmp:size=64m,noexec,nosuid,nodev`); [size it](#tmp) to your database and plugins |
| Volume | `/config`, which you mount, owned by 65532; and your [music and downloads](#media-and-hardlinks) |
| Processes | one: `Lidarr`, as PID 1, which runs `fpcalc` on audio files; plugins may run `ffmpeg` and `ffprobe` |
| Shell in image | none |
| Package manager | none |
| Outbound network | none needed to serve or be healthy; [Lidarr reaches out at rest](#egress-at-rest) |
| Healthy | the shipped probe: `/ping` 200 **and** `GET /api/v1/system/status` reporting Lidarr at this image's version |

## Running

Pull `ghcr.io/divbast/lidarr:latest`, which follows Lidarr's nightlies
until v4 stable, as [The nightly line](#the-nightly-line) says; a version
tag, `:<version>` with the Lidarr build in place of `<version>`, pins one.
The package page on ghcr.io lists the tags. To build it yourself: `docker
login dhi.io`, since every image the build starts from is Docker Hardened,
then `just setup`, then `ENGINE=podman just build`, which tags it
`localhost/lidarr:dev`. Substitute that tag for
`ghcr.io/divbast/lidarr:latest` in the command below.

```bash
podman run -d --name lidarr \
  --cap-drop ALL --read-only --read-only-tmpfs=false \
  --security-opt no-new-privileges \
  --user 65532:65532 \
  --tmpfs /tmp:size=64m,noexec,nosuid,nodev \
  -v lidarr-config:/config \
  -v /srv/data:/data \
  -p 127.0.0.1:8080:8080 \
  --health-cmd '["/usr/local/bin/divbast-lidarr","health"]' \
  ghcr.io/divbast/lidarr:latest
```

`--read-only-tmpfs=false` and `--health-cmd` are Podman's alone. Without
the first, Podman mounts writable tmpfs of its own at `/run`, `/var/tmp`
and elsewhere beside the read-only root; without the second, it never
runs the image's health check, as [Health](#health) says. Docker takes
the same command without them, and runs the image's `HEALTHCHECK` itself.

Lidarr serves its UI and API on 8080. **A fresh install asks for no
login**, as upstream ships it: until you choose an authentication method,
anyone who can reach the port can use the UI, read the API key and
download a backup. Keep the port on the host's loopback, as above, and
choose one at your first visit, as [Authentication](#authentication) says,
before you publish it anywhere else. A stop (SIGTERM) ends Lidarr in well
under a second, with exit 0. Restarting it from its UI, which installing
a plugin asks for, happens inside the same process, so the container keeps
running.

### `/config`

The image carries an empty `/config` owned by 65532, and declares no
`VOLUME`. A new named volume takes its owner from it on first mount, so
`-v lidarr-config:/config` just works. Lidarr keeps everything it writes
there: `config.xml`, its databases, logs, backups, artwork and plugins.

- A `/config` that 65532 cannot write, such as a bind mount root made, or a
  volume an image run as root left, makes Lidarr exit 1 within two
  seconds, with `Access to the path '/config/Sentry' is denied` in its
  log: its error reporter's folder is the first thing it writes. Give the
  directory, and everything in it, to 65532 instead of running the image
  as root: `chown -R 65532:65532 <dir>` as root, or under rootless Podman
  `podman unshare chown -R 65532:65532 <dir>`. For a Podman bind mount,
  the `:U` option does the same at start: `-v ./config:/config:U`.
- On an SELinux host, such as Fedora or RHEL, a bind mount also needs
  Podman to relabel it, or the container cannot use it: add `:Z`, as in
  `-v ./config:/config:U,Z`. `/config` is Lidarr's alone, so the private
  label `Z` fits it.
- With no mount at all, the read-only root refuses that first write, and
  Lidarr dies within a second, logging `Read-only file system :
  '/config/Sentry'`. Its exit status is the runtime's signal, 133 on arm64.
  Nothing would have been kept.
- A `config.xml` Lidarr cannot read, cut off by a full disk or a crash,
  makes it exit 3 within seconds, saying `/config/config.xml is corrupt or
  invalid`. The image passes `-exitimmediately`; without it Lidarr waits an
  hour for a key press no one can make in a container.
- **Some startup failures exit 0.** Upstream catches a failure inside its
  web host, such as a port already in use, a corrupt database or a PID
  file it cannot write, logs `Lidarr failed to start: …`, and exits
  normally. A restart policy of `on-failure` does not restart such a
  container; read its log.

### Coming from another image

**Back up first.** A linuxserver or hotio `/config` works here, but each
nightly may add a database migration, and no older Lidarr opens a database
a newer one migrated: keep the old `/config`, or a backup from the old
install's System, Backup, until you are sure.

Copy the old `/config` into the new volume or directory, then give it all
to 65532 as above, with `chown -R`: the files another image wrote are
usually its PUID's, such as 1000. Or start fresh and restore the backup
under System, Backup; Lidarr restarts itself, in the same container, to
apply it.

**After restoring a backup, restart the container** (`podman restart
lidarr`) before you change anything. Lidarr's own restart puts the
restored database in place, but upstream leaves SQLite's connections open
across that restart, so Lidarr can go on reading, and writing, the
database the restore replaced: the restore looks undone, and whatever you
change before a full restart is lost. A restart of the container opens
the restored database whatever was left open.

- This image serves on 8080, not Lidarr's usual 8686, and its
  `LIDARR__SERVER__PORT` wins over the old `config.xml`. Whatever reaches
  Lidarr by its address, Prowlarr's Lidarr application among them, needs
  the new one, or the old number published to the new port
  (`-p 127.0.0.1:8686:8080`).
- The old install's branch, such as `master`, becomes `nightly` on the
  first start, which Lidarr logs; its authentication stays as it was.
- Your music and download folders must be mounted at the paths the old
  install used, or remapped in Lidarr, and writable by 65532:
  [Media and hardlinks](#media-and-hardlinks).

### `/tmp`

`/tmp` is the contract's one tmpfs, and Lidarr uses it to stage a backup,
copying the database there before it zips the backup into `/config`, to
receive a backup you restore, and to download a plugin before it installs
it. So `/tmp` must hold twice `lidarr.db`, and the largest plugin you
install, or the backup, the weekly scheduled one included, fails.

64 MiB, the size above and the one the acceptance suite runs under, is far
more than a new install needs: a backup of a 600 KiB database fitted in
1 MiB, and so did its restore. A library of thousands of albums makes a far
larger database, and a plugin's zip can be tens of megabytes. To size it
for your own, read the size of `lidarr.db` in the `/config` volume, with
`ls -l` on the host path `docker volume inspect` or `podman volume
inspect` gives, double it, add your largest plugin, and keep to 64 MiB or
more. For example, a 300 MiB database wants
`--tmpfs /tmp:size=640m,noexec,nosuid,nodev`. A tmpfs counts against the
container's memory, and fills only while a backup, restore or install
runs, so size it for those and no further.

## Configuration

Any `config.xml` key can be set as `LIDARR__<SECTION>__<KEY>`, and the
environment wins over the file. The image sets `LIDARR__SERVER__PORT=8080`,
which also wins over a `config.xml` brought from another image that says
8686. **Values are case-sensitive:** `LIDARR__AUTH__METHOD=forms` is
ignored without a word, and authentication stays off; write `Forms`. The
keys most needed:

| Variable | What it does |
|---|---|
| `LIDARR__AUTH__METHOD` | `Forms`, a login page; `External`, behind a proxy that authenticates; `None`, upstream's default on a fresh install ([Authentication](#authentication)) |
| `LIDARR__AUTH__REQUIRED` | `Enabled`, or `DisabledForLocalAddresses` |
| `LIDARR__AUTH__APIKEY` | The API key, over the one in `config.xml` |
| `LIDARR__SERVER__URLBASE` | The path a reverse proxy serves Lidarr under, such as `/lidarr` |
| `LIDARR__SERVER__TRUSTEDNETWORKS` | Comma-separated networks, in CIDR form, whose `X-Forwarded-For`, `-Proto` and `-Host` headers Lidarr trusts |
| `LIDARR__LOG__ANALYTICSENABLED` | `false` turns [analytics](#analytics) off |
| `LIDARR__POSTGRES__HOST`, `__PORT`, `__USER`, `__PASSWORD`, `__MAINDB`, `__LOGDB` | [PostgreSQL](#postgresql) in place of SQLite |

### Authentication

A fresh install's method is `None`, with authentication "required": the UI
opens on a dialog asking you to choose a method, which the server does not
enforce until you do. There is no first user and no first password to read
from the log. At your first visit, choose **Forms** and set a user name and
password; the login applies at once, and from then on the UI, backup
downloads and `/initialize.json`, which hands the UI the API key, send a
visitor with no session to the login page. Until then, all three answer
anyone.

Set `External` instead when Lidarr sits behind a proxy that authenticates
every request itself: `External` makes Lidarr let in everyone who reaches
it, so nothing but that proxy may. `Basic` runs as Forms.

The probe does not mind which method is in force: it sends the API key,
which every method accepts.

### Behind a reverse proxy

Set `LIDARR__SERVER__TRUSTEDNETWORKS` to the proxy's address or network,
such as `172.18.0.0/16`, so that Lidarr takes the client's address and
scheme from the headers the proxy sends, and not the proxy's own. Without
it, every request looks to be from the proxy.

**Do not use `LIDARR__AUTH__REQUIRED=DisabledForLocalAddresses` behind a
proxy** unless the proxy sets `X-Forwarded-For` and is listed in
`LIDARR__SERVER__TRUSTEDNETWORKS`. Lidarr treats a client as local when
its address is local and the request carries no `X-Forwarded-For` header.
A proxy on a private network that sends none makes every proxied client
local, and authentication is skipped for everyone who can reach the proxy.

If the proxy serves Lidarr under a path, set the URL base, in the
environment or in the UI. Lidarr answers nothing at the bare path but a
redirect. The health probe reads the base from the environment, else from
`config.xml`, so either works, and it never follows a redirect.

### Analytics

Analytics are on, as upstream ships them. They and the error reports
Lidarr sends to Sentry go to Servarr. Turn them off in the UI, under
Settings, General, or with `LIDARR__LOG__ANALYTICSENABLED=false`.

### Updates

Lidarr's own updater is off. The image's `/app/package_info` names Docker
as the update mechanism, on the `nightly` branch, so the UI offers no
update, and a `config.xml` brought from another install that says
`master` is switched at start. Lidarr's health page may still say a newer
nightly exists: it arrives as a new image, and you update by pulling it.

## The nightly line

Every build of Lidarr's develop branch that upstream's CI completes is a
nightly, and upstream publishes them from Azure DevOps, not on GitHub,
until v4 is stable. Renovate reads Azure's public build list, takes each
new nightly that built from develop and passed, once the acceptance suite
passes on it here, and the daily slot publishes it as `:latest` and its
version, such as `:4.0.0.5227`. Nightlies are builds upstream has not
promoted: expect their changes, and keep backups.

Two things follow from where the nightlies come from. A nightly has no
tag, so the build trusts the commit hash Azure records for it, and checks
nothing more. And if upstream ever moves its builds off Azure, as Sonarr
did in 2024, the list just stops growing: the image stays on its last
nightly, and no check turns red until someone notices that no update has
come.

When upstream publishes v4 stable, the image stops taking nightlies and
stays on the last one it has, until it moves to stable: a check holds
Renovate's pull request red, and the move is made by hand, to a stable
release that carries every database migration that nightly has, so that no
database ever meets an older Lidarr than the one that migrated it. Then
`:latest` follows Lidarr's stable releases, and you change nothing.

## Plugins

Lidarr 4 installs plugins from GitHub, under System, Plugins, given a
plugin's repository URL. It downloads the release into `/tmp/plugins`,
installs it under `/config/plugins/<owner>/<name>`, and loads it once it
restarts, which it does in the same container. A plugin that breaks
startup makes Lidarr start without plugins and raise a health error.

- **Only plugins built for .NET 10 install.** Lidarr takes the release
  asset named for its own runtime, `net10.0`, and on this image most
  plugins publish only `net8.0` so far: they fail with `No compatible
  release found`. One was proved here, by hand: the maintained Deezer
  fork, `https://github.com/jasonpatrickellykrause/Lidarr.Plugin.Deezer`,
  10.2.0.78, installed from System, Plugins under the image's full
  confinement, loaded after the in-process restart, added its Deezer
  indexer and download client, and left the Health page clean. Its
  install peaked at under 3 MiB of `/tmp`.
- **Plugins are third-party code** running inside Lidarr with its full
  access to its data and its network. divbast neither reviews nor tests
  them.
- Installing needs `api.github.com`, `github.com` and
  `release-assets.githubusercontent.com`, and GitHub's limit on anonymous
  API calls applies.
- A plugin that downloads tools of its own and runs them needs `/config`
  mounted without `noexec`, and those tools are outside the image's scans.
  `ffmpeg` and `ffprobe` are already on `PATH`, as below, and Tubifarry
  uses those rather than fetching its own.

## ffmpeg and ffprobe

Lidarr itself never runs ffmpeg; its plugins do, to transcode what they
download. The image carries `ffmpeg` and `ffprobe` in `/usr/local/bin`,
built from FFmpeg's source at the release the Containerfile's
`FFMPEG_VERSION` names, for the formats plugins meet and nothing else:

- **Read:** MP3, AAC, Opus, Vorbis, WMA (v1, v2, Pro), AC-3, E-AC-3 and
  DTS; FLAC, ALAC, WavPack, Monkey's Audio, TrueHD and WMA Lossless;
  uncompressed PCM in WAV, AIFF, CAF or W64; in the containers those come
  in; and cover art as JPEG, PNG, BMP, GIF, TIFF or WebP.
- **Write:** MP3 (LAME), AAC, Opus, Vorbis, FLAC and ALAC, in MP3, M4A,
  MP4, Ogg, Opus, FLAC and ADTS files, and JPEG cover art.
- No video beyond cover art, no network, no devices. A file in any other
  format, such as DSD, Musepack or an ADPCM WAV, is refused.

MP3, Opus and Vorbis come from Debian's LAME, Opus and Vorbis libraries,
DHI's builds of them, linked into the binaries. The build is LGPL. Each
binary is about 11 MB, and transcoding is quick: on an arm64 Mac, a
60-second test tone became MP3 in about a quarter of a second, Opus or
Vorbis in about half a second, and AAC in about a second.

**The scan cannot see them.** A static build carries no package record, so
a vulnerability in FFmpeg or its codec libraries blocks nothing here until
Renovate moves the pin. The narrow, hardened build is the mitigation:
decoders and demuxers are where FFmpeg's vulnerabilities are found, this
one has only the few above, and it is built with FFmpeg's own exploit
mitigations (`--toolchain=hardened`: FORTIFY_SOURCE, stack protection,
position-independent binaries bound at load).

**fpcalc carries an FFmpeg of its own.** `/app/bin/fpcalc`, the
fingerprinting tool Lidarr itself runs on every file it imports, comes
from upstream's pin of Chromaprint on NuGet, statically linked with a
whole FFmpeg (libavcodec 63.1 at this pin). This image neither narrows
nor hardens that one, the scan cannot see it either, and it moves only
when upstream moves the package. To stop Lidarr running it, set Allow
Fingerprinting to Never under Settings, Media Management (shown with
the advanced settings), at the cost of AcoustID matching.

## Egress at rest

The contract's no-egress check holds: the image serves and is healthy on a
network with no route out. Lidarr, though, reaches out while it runs, as
upstream ships it:

- `lidarr.servarr.com`, for upstream's notices, the time and update checks.
  With no route out, Lidarr logs each failure, and carries on.
- `api.lidarr.audio`, for artist and album metadata, and the images it
  names, which Lidarr keeps under `/config/MediaCover`. Without it, no
  artist can be added or refreshed.
- Analytics and error reports go to Servarr, until turned off.
- Plugins add GitHub, as above, and whatever services they use.
- Then whatever indexers, download clients, lists and connections you
  configure.

None of it is needed to serve or to be healthy. One thing does change
with no route out, once 14 days have passed since the image was built,
as they soon do for a published image: Lidarr then checks for an update
with every round of its health checks, the check fails, and the round
shows nothing, so the UI's System, Status page is
empty rather than listing the warnings it would. The probe stays healthy;
give Lidarr a route to `lidarr.servarr.com` to see the page.

## Not supported

There is no shell in the image, so these cannot work:

- Custom Script connections.
- Import Using Script.
- The Script update mechanism.
- Any other interpreted script.

A static binary on a mount still runs, unless you mount it from somewhere
`noexec`. `fpcalc`, which Lidarr runs to fingerprint audio files, is a
static binary in the image itself.

## Media and hardlinks

Lidarr imports what your download client finishes into your library, so
it needs both, mounted and writable by 65532. Hardlinking, which imports
instantly and without a second copy, needs the source and the destination
on one filesystem, so give Lidarr and the download client one shared
`/data` mount, such as `/data/downloads` and `/data/music` beside each
other, not a mount each. Under `--cap-drop ALL`, the kernel's
protected-hardlinks rule lets a process link only a file it owns or can
read and write; when 65532 can do neither, Lidarr copies the file instead,
so run the download client as 65532 too, or give 65532 its files. On an
SELinux host, a `/data` bind mount several containers share takes Podman's
`:z`, the shared label, not `:Z`, which would keep it to one container.

## PostgreSQL

Lidarr can use PostgreSQL, 15 or later upstream says, through the
`LIDARR__POSTGRES__*` variables above. This image has not been tested
with it.

## Health

A server that answers can still be broken, so the image ships its own probe
and declares a `HEALTHCHECK`: `--interval=30s --timeout=10s
--start-period=300s --retries=3`. The start period is long because Lidarr
answers nothing until its database migrations finish, and a nightly's
migrations on a large library can be slow; the first check that passes
ends it, so a healthy start loses nothing. Docker runs the check with no
configuration.

Podman does not. Podman's `libimage` reads a healthcheck only out of a
Docker-media-type manifest, and divbast images are published as an OCI
index, so Podman's inspection path never looks at the config's healthcheck
field — [podman/podman#25454](https://github.com/containers/podman/issues/25454)
and [#18904](https://github.com/containers/podman/issues/18904) track it. A
Podman user passes it explicitly, with `--health-cmd` as in
[Running](#running). Under a Quadlet, the equivalent is:

```
HealthCmd=["/usr/local/bin/divbast-lidarr","health"]
```

The array above has no leading `"CMD"` on purpose: Podman before 5.8.0
re-splits an array that starts with `"CMD"` into one useless token, so
omitting it is what makes the check work across versions. Podman takes
none of the image's timings either, and by default allows no start
period: add `--health-start-period=300s` (`HealthStartPeriod=300s` in a
Quadlet) if migrations could outlast three of its checks.

| | |
|---|---|
| `/usr/local/bin/divbast-lidarr health` | The in-container probe `HEALTHCHECK` runs. Asserts that `/ping` answers 200 with `"status": "OK"` **and** that `GET /api/v1/system/status`, with the API key, says `appName` is `Lidarr` and `version` is this image's. Prints nothing when healthy; otherwise one line, `ping: <reason>` or `status: <reason>`, for each half that failed, and exits 1 |
| `/usr/local/bin/divbast-lidarr version` | Prints the version |

`/ping` runs a database query, so it shows the server and its database
answer; only the status shows the build is the pinned one. The probe reads
the API key from `LIDARR__AUTH__APIKEY`, else `/config/config.xml`, and
fails with a line saying so when it finds neither. Lidarr makes a key for
itself on its first start, so a plain run needs nothing set.

There is no shell in this image, so a health command given as a plain
string, which Docker and Podman both run through `/bin/sh -c`, can never
pass.

## The pipeline

Three workflows, each a short caller of divbast/ci's reusable workflow of
the same purpose, at one pinned release of it:

- `build.yml` calls `app-check.yml` on every push, pull request, daily slot
  and button run: lint, the image built for amd64 and arm64 once every
  image the Containerfile builds from is verified, the acceptance suite
  against each under Docker and rootless Podman, and the scan. Its
  `required` job is the one check `main`'s ruleset requires. From the daily
  slot and the button, on `main`, it then calls `app-publish.yml`.
- `renovate.yml` runs Renovate, as below.
- `stall.yml` fails, and GitHub emails you, when a Renovate pull request
  has been open more than 14 days or red or pending for more than a day,
  when Renovate has not succeeded in a day, or when it could not look a
  dependency up, Azure's build list among them.

The acceptance suite, on top of the contract, checks that Lidarr serves
`/ping` and its login page, refuses the API without a key, starts with no
login asked for, is healthy under every authentication method and URL
base, signs its first user in through the login form and refuses a wrong
password, stops on SIGTERM and exits 0, keeps its state across a restart,
restarts from its UI inside the process with its plugins folder kept,
reports Docker as its updater on `nightly`, refuses an unwritable or
missing `/config` and a corrupt `config.xml`, runs on a `/config` a
linuxserver or hotio image left, makes and serves a backup within the
`/tmp` it is given, restores one through the UI's upload, runs `fpcalc`
with no complaint on its Health page, transcodes with `ffmpeg` and reads
the results with `ffprobe`, and lists no plugins on a fresh install.

### Publishing

A merge never publishes. The daily slot publishes `main` when `main` moved
since the commit the published `:latest` was built from, and the button, a
manual run of `build` from `main`, publishes at once.

Its publish job calls divbast/ci's `app-publish.yml` with `image:
ghcr.io/divbast/lidarr`. A publish pushes an index of the amd64 and arm64
images, attests it, verifies the attestation, and only then tags it twice:
with Lidarr's version, the Containerfile's `LIDARR_VERSION`, and with
`latest`. It ends by checking, as an anonymous client, that both tags are
the attested index.

## Dependency updates

Every input the Containerfile names is pinned: the DHI runtime, and the
DHI Node, .NET SDK and Go images the stages build on, by digest; Lidarr's
version and commit, together in two adjacent `ARG`s; FFmpeg's release and
commit, likewise; and the six DHI packages the ffmpeg stage installs, by
exact version. The probe is the standard library alone, so `go.mod`
requires nothing but divbast/ci's harness for its tests.

A pin only moves through a pull request, and
[Renovate](https://docs.renovatebot.com/) opens those. It runs every
morning, after every merge to `main`, whenever CI finishes on one of its
branches and when the Dependency Dashboard issue is edited, from
`.github/workflows/renovate.yml`, as the divbast org's `divbast-renovate`
GitHub App. Its settings are divbast/renovate-config's shared presets
(`:app` among them), and `renovate.json` adds what they cannot know. The
pull requests Renovate opens here are:

- A new Lidarr nightly moves `LIDARR_VERSION` and `LIDARR_COMMIT`
  together, read from Azure DevOps' build list: only builds of develop
  that passed, never a pull request's.
- Upstream's first v4 stable release moves the Containerfile's
  `LIDARR_STABLE_V4` line, in the same pull request as the nightly pins,
  which a check then holds red, and which never merges itself:
  [The nightly line](#the-nightly-line).
- A new FFmpeg release moves `FFMPEG_VERSION` and `FFMPEG_COMMIT`
  together, from FFmpeg's release tags, never its development ones; the
  build fails if the tag ever names another commit.
- A new DHI build of LAME, Opus, Vorbis, Ogg, pkgconf or zlib moves their
  pins together, from DHI's own Debian repository. DHI deletes a
  superseded build, so until that pull request merges, the build is red.
- A new DHI image arrives as a pull request that moves its digest, and CI
  verifies Docker's signature before it builds. The .NET SDK and the
  ASP.NET Core runtime move together in one pull request, and a new .NET
  major waits for approval on the Dependency Dashboard.
- A release of divbast/ci arrives as one pull request that moves every
  workflow's pin of it together.
- The toolchain in `mise.toml` moves by pull requests of its own.

Renovate merges its own pull requests once every check on one has passed,
while the repository variable `LIDARR_AUTOMERGE` is `enabled`. Three
kinds first wait for someone to approve them on the Dependency Dashboard:
a new .NET major, a new major version of divbast/ci, and a raise of
`go.mod`'s `go` line.
Any other value of the variable, or none, stops merging from the next run,
and Renovate goes on opening and rebasing pull requests for a person to
merge:

    gh variable set LIDARR_AUTOMERGE --body disabled

After a merge it rebases every open update the merge left behind, so each
is tested again against the new `main` before it merges. It merges with a
squash, which GitHub signs, because `main` accepts only signed commits.

The App, the `renovate` environment holding its key, `LIDARR_AUTOMERGE`
and this repository's rulesets are all managed from
[divbast/infra](https://github.com/divbast/infra).

## Development

- `just setup` fetches divbast/ci into `.ci/` at the commit the workflows
  pin, then installs the pinned toolchain from `mise.toml`.
- `just build` builds the image locally, without verifying the images it
  builds from, and needs `docker login dhi.io`; `just ci-tools &&
  .ci/bin/divbast-verify-images` does verify them, with cosign from
  `mise.toml`.
- `just scan` reads the published image with
  [skopeo](https://github.com/containers/skopeo), which must be on `PATH`;
  so must it for `just ci`. CI runners already have it.
- `just test` builds, then runs the acceptance suite against the image,
  under `ENGINE`, docker unless it says podman. Under Docker it needs
  `zstd` on `PATH`, since every layer of a Docker Hardened Image is zstd
  and the suite reads the image's layers through it; no `mise.toml` entry
  provides it. CI runners already have it, and a Mac gets it with `brew
  install zstd`.
- `just lint` runs every lint gate, and needs a running Docker daemon, for
  `docker build --check`.
- `just ci` runs every gate, the scan included. Run it before committing.

The recipes that call GitHub, pinact's and zizmor's, read a fine-grained
personal access token from `GITHUB_TOKEN`: divbast/ci's README says which,
and how to pass it.

## AI/LLM Disclosure

This project was developed with significant LLM involvement. Each git commit contains an `Assisted-by:` tag detailing the particular model/tool used.
