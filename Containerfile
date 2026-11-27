# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Renovate moves these two in one PR; its regex needs them adjacent and in
# this order. They follow Lidarr's nightly builds, every build of develop
# upstream's CI completes, read from Azure DevOps' public build API, the one
# source that pairs a nightly's version with its commit, until Lidarr v4
# stable exists: develop is Lidarr 4, on .NET 10, and publishes nothing on
# GitHub yet. The version is all four numbers: the image is tagged with it
# (app-publish.yml reads it from this line, by the name build.yml gives as
# version-arg), the assemblies carry it, and the probe compares it with the
# version Lidarr reports. A nightly has no tag, so the fetch stage checks out
# the commit and checks nothing more: the full commit fixes the content, and
# the version beside it is Azure's record of that build, which Renovate
# writes into both lines in one change.
ARG LIDARR_VERSION=4.0.0.5236
ARG LIDARR_COMMIT=beafd022d5c7023576f00c7b45756d5d539b7a36
# The watch for Lidarr's first stable v4 release, which no build reads:
# Renovate writes it on the line below once GitHub has a v4 release that is
# not a pre-release, and test/stable's TestNoV4StableYet then fails, so the
# nightly line stops here until the image moves to stable: CLAUDE.md,
# 'Moving to stable'. 0.0.0.0 stands for none.
# LIDARR_STABLE_V4=0.0.0.0
# FFmpeg, built from source for Lidarr's plugins: Renovate moves these two
# in one PR, from FFmpeg's release tags, and its regex needs them adjacent
# and in this order. The ffmpeg stage fetches the commit and asserts the
# tag still names it.
ARG FFMPEG_VERSION=n9.1
ARG FFMPEG_COMMIT=74e4393595cf2b5857f0da925c4e0dd74ea306e5
# The runtime: Docker Hardened Images' ASP.NET Core on Debian 13, with ICU,
# OpenSSL and the .NET runtime the app runs on, no shell and no package
# manager, and its nonroot user 65532. The app is built framework-dependent,
# so a rebuild of this image patches the runtime. CI verifies its signature
# by Docker before anything is built, as it does every stage's image below.
ARG RUNTIME_IMAGE=dhi.io/aspnetcore:10.0.15-debian13@sha256:ae9e6b90e72d386ed60d562f169305f5323c6e079dee72bd30980b086069bc8c

# The same image as the frontend's, which ships git and CA certificates.
FROM dhi.io/node:26.13.1-debian13-dev@sha256:efd29755a80e4753b972c58d0adeb3d45874c45d200d9ab8a17acf9dbb73620a AS fetch
ARG LIDARR_COMMIT
# git checks a fetch by commit against its hash. There is no tag to check
# it against: a nightly has none, and an ancestry check against develop
# would turn the build red, and hold back the base image's patches, each
# time upstream rewrites develop. A fetch by commit never depends on the
# branch while GitHub keeps the commit. The fetch gets three retries, 10s,
# 30s and 90s apart, so one dropped connection does not cost the daily
# slot. The remote is named so that the .NET build's Source Link finds the
# repository it came from.
RUN set -eux; \
    retry() { \
        for delay in 10 30 90; do "$@" && return 0; sleep "${delay}"; done; \
        "$@"; \
    }; \
    git init -q /src; \
    git -C /src remote add origin https://github.com/Lidarr/Lidarr.git; \
    retry git -C /src fetch -q --depth 1 origin "${LIDARR_COMMIT}"; \
    git -C /src checkout -q --detach FETCH_HEAD; \
    head=$(git -C /src rev-parse HEAD); \
    test "${head}" = "${LIDARR_COMMIT}"

# The UI, as upstream's CI builds it, with webpack, into _output/UI. yarn
# 1.22.22 ships in the image, and yarn.lock pins every package by its
# sha512. No dependency's install script runs: Lidarr's own package.json
# has none, the build needs none, and an install script is how most
# malicious npm releases have run, so a compromised package that reaches
# yarn.lock through upstream has one way fewer to act. NODE_ENV stays unset
# for the install, where production would skip the devDependencies webpack
# is among; --env production is the build's own switch.
FROM dhi.io/node:26.13.1-debian13-dev@sha256:efd29755a80e4753b972c58d0adeb3d45874c45d200d9ab8a17acf9dbb73620a AS frontend
WORKDIR /src
COPY --from=fetch /src ./
RUN yarn install --frozen-lockfile --ignore-scripts --network-timeout 120000 \
    && yarn run build --env production

# The backend, framework-dependent for this stage's own architecture.
FROM dhi.io/dotnet:10.0.404-sdk-debian13@sha256:19e972837ae5d0e55a9c4eb677f6b20382719900f7de85acaa83d0b243dbbef5 AS backend
ARG LIDARR_VERSION
ARG TARGETARCH
# Upstream keeps no lock file, and pins every package by an exact version
# from nuget.org alone, which signs each one: NuGet checks those signatures
# here.
ENV DOTNET_CLI_TELEMETRY_OPTOUT=1 \
    DOTNET_NUGET_SIGNATURE_VERIFICATION=true
SHELL ["/bin/bash", "-euo", "pipefail", "-c"]
WORKDIR /src
COPY --from=fetch /src ./
COPY test/global-json.sh /tmp/global-json.sh
# The one change made to an upstream file, which never leaves this stage.
# global.json pins the SDK with no roll-forward policy, which accepts only
# that feature band, so an SDK of a later band, which Renovate moves this
# stage's image to, would refuse to build. The script keeps upstream's
# version as the floor and accepts any later band of the same major and
# minor, after checking that major and minor are this image's: a .NET
# major on one side alone fails here, with the steps to take. dotnet asked
# from / reports the image's own SDK, since no global.json is in the way
# there; asked from the source, it would refuse before the rewrite.
# hadolint's DL3003 asks for WORKDIR in place of that cd, which lasts for
# the one command substitution: WORKDIR would move the whole RUN away from
# the source.
# hadolint ignore=DL3003
RUN sdk=$(cd / && dotnet --version); /tmp/global-json.sh global.json "${sdk}"
# The properties restore and publish share, one per line, in a response
# file both read. The version is passed rather than left at upstream's
# 10.0.0.*, which runs as a development build, with analytics and Sentry
# forced on whatever the user sets, and which passes every plugin's
# minimum-version check; -p: overrides it, and the branch, without editing
# Directory.Build.props. The branch is develop, the one the nightlies are
# built from and upstream's own build stamps, until the image moves to
# stable: CLAUDE.md, 'Moving to stable'. The publish is for this stage's
# runtime identifier, so NuGet's runtimes/<rid>/native/fpcalc lands beside
# the app. Upstream treats every warning as an error, NuGet's audit of
# known advisories among them, so a new advisory against any package an
# already-pinned commit restores would fail the build, and hold back every
# update, the runtime's patches included, until upstream moves. Those four
# stay warnings here, printed in the build log: ci's scan already blocks a
# vulnerable package the image does not already ship. %3B is msbuild's
# escaped semicolon, which keeps the four in one property.
RUN case "${TARGETARCH}" in \
        amd64) rid=linux-x64 ;; \
        arm64) rid=linux-arm64 ;; \
        *) echo "no .NET runtime identifier for ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    printf '%s\n' -p:Configuration=Release -p:Platform=Posix -p:SelfContained=false \
        "-p:RuntimeIdentifiers=${rid}" "-p:AssemblyVersion=${LIDARR_VERSION}" \
        -p:AssemblyConfiguration=develop -p:EnableAnalyzers=false -p:SentryUploadSymbols=false \
        -p:WarningsNotAsErrors=NU1901%3BNU1902%3BNU1903%3BNU1904 \
        >/tmp/build.rsp; \
    printf '%s\n' "${rid}" >/tmp/rid
RUN dotnet msbuild src/Lidarr.sln -t:Restore @/tmp/build.rsp
# Everything it needs was restored above, so the publish runs offline.
RUN --network=none dotnet msbuild src/Lidarr.sln -t:PublishAllRids @/tmp/build.rsp
# /app/bin as upstream's Linux package lays it out, without the Windows and
# service helpers, and without Lidarr.Update, the in-app updater the image
# never runs, which publish writes to a folder of its own: package_info
# names Docker as the update mechanism, and upstream reads it only from
# beside a directory named bin. Publish writes the app under its target
# framework, _output/net10.0 today, which a .NET major moves, so the folder
# is found rather than named, and must be the only one: none means publish
# wrote it somewhere else, and with several nothing says which is the app.
# nullglob, for the find alone, makes no match an empty list rather than
# the pattern itself. NuGet extracts fpcalc, which Lidarr runs to
# fingerprint audio files, without its execute bit; without it Lidarr's
# health check reports an old fpcalc, not a missing bit. It is a static
# binary, so it needs nothing else from the base. Branch is nightly, the
# channel the image follows, so upstream's update check compares against
# the right builds, until the image moves to stable: CLAUDE.md, 'Moving to
# stable'.
RUN rid=$(</tmp/rid); \
    shopt -s nullglob; \
    published=(_output/net*/"${rid}"/publish); \
    shopt -u nullglob; \
    if ((${#published[@]} != 1)); then \
        echo "ERROR: ${#published[@]} folders match _output/net*/${rid}/publish, where publish writes the app to one: ${published[*]:-none}." >&2; \
        exit 1; \
    fi; \
    mkdir -p /out/config; \
    cp -a "${published[0]}" /out/bin; \
    rm /out/bin/Lidarr.Windows.* /out/bin/ServiceInstall* /out/bin/ServiceUninstall*; \
    test ! -e /out/bin/Lidarr.Update; \
    chmod 0755 /out/bin/fpcalc; \
    test -x /out/bin/fpcalc; \
    printf '%s\n' "PackageVersion=${LIDARR_VERSION}" \
        'PackageAuthor=[divbast](https://github.com/divbast/lidarr-oci-image)' \
        UpdateMethod=Docker Branch=nightly >/out/package_info
COPY test/runtime-major.sh /tmp/runtime-major.sh
# With no default of its own, RUNTIME_IMAGE takes the global one at the
# top of this file, which the runtime stage is built FROM. It is declared
# here, for the check alone: BuildKit gives every ARG in scope to each
# later RUN as its environment, and so to its cache key, and declared any
# higher it would have a new runtime pin rebuild the restore and publish.
# It stands alone, with nothing after it on its line: Renovate reads ARG
# NAME VALUE as NAME=VALUE, so anything there would become the runtime
# pin it moves.
ARG RUNTIME_IMAGE
# An app built for one .NET does not start on another's runtime, and
# nothing else would say so: the script compares the .NET the shipped
# runtimeconfigs name with the one leading the runtime image's tag, and
# fails a mismatch with the steps to take. It reads /out/bin once nothing
# more is written there, so what it passes is what ships.
RUN /tmp/runtime-major.sh /out/bin "${RUNTIME_IMAGE}"

# The image's health probe, static and on the standard library alone.
FROM dhi.io/golang:1.27.4-debian13-dev@sha256:121afb8b390b1d6babba8cd0a93eaf2ffa3735f835d388b9d79c2be38b41ee4f AS probe
ARG LIDARR_VERSION
WORKDIR /src
# go.mod's replace names ./.ci, which the build context leaves out: the
# probe imports nothing from divbast/ci, so Go never looks for it
COPY go.mod ./
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOFLAGS=-mod=readonly GOTOOLCHAIN=local \
    go build -trimpath -ldflags "-s -w -X main.version=${LIDARR_VERSION}" \
    -o /out/divbast-lidarr ./cmd/divbast-lidarr

# ffmpeg and ffprobe, which Lidarr itself never runs and its plugins do:
# Tubifarry, among others, transcodes downloads with them, and finds them
# on PATH rather than fetching a build of its own into /config. The Go
# -dev image, which the probe stage already pulls, has gcc, make, readelf,
# git and apt, with Docker Hardened Images' own Debian repository beside
# Debian's.
FROM dhi.io/golang:1.27.4-debian13-dev@sha256:121afb8b390b1d6babba8cd0a93eaf2ffa3735f835d388b9d79c2be38b41ee4f AS ffmpeg
ARG FFMPEG_VERSION
ARG FFMPEG_COMMIT
ARG TARGETARCH
SHELL ["/bin/bash", "-euo", "pipefail", "-c"]
# The codec libraries for MP3, Opus and Vorbis, which FFmpeg cannot encode
# well alone: it has no MP3 encoder, and its own Opus and Vorbis encoders
# are experimental. Each is pinned, one name=version a line, which
# Renovate's manager reads, to a +dhi build, which only DHI's repository
# has, so apt never falls back to Debian's. DHI deletes a superseded
# build, so a pin can fail the build until Renovate's PR moves it; these
# packages rarely change. Each library's .so link is removed, leaving its
# static archive for the linker, so the codecs link into the binaries:
# the runtime base carries none of them. glibc and zlib stay shared, from
# the base, where the scan and DHI's VEX cover them.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        libmp3lame-dev=3.100-6+deb13u0+dhi0 \
        libogg-dev=1.3.5-3+deb13u0+dhi0 \
        libopus-dev=1.5.2-2+deb13u0+dhi0 \
        libvorbis-dev=1.3.7-3+deb13u0+dhi0 \
        pkgconf=1.8.1-4+deb13u0+dhi1 \
        zlib1g-dev=1:1.3.dfsg+really1.3.1-1+deb13u1+dhi0 \
    && rm -rf /var/lib/apt/lists/* \
    && lib=/usr/lib/$(gcc -dumpmachine) \
    && for name in mp3lame ogg opus vorbis vorbisenc; do \
        test -f "${lib}/lib${name}.a"; \
        rm "${lib}/lib${name}.so"; \
    done
# Fetched by commit, which git checks against its hash, and the tag asked
# for separately, peeled, since FFmpeg's are annotated, so a tag moved
# upstream fails the build. FFmpeg signs its tags; the image does not
# check the signature, since a new key would be a pin a person moves, and
# the commit pin already fixes the content. .git goes once checked, so
# FFmpeg's version script reads the version from RELEASE instead of
# naming the shallow clone's commit.
RUN retry() { \
        for delay in 10 30 90; do "$@" && return 0; sleep "${delay}"; done; \
        "$@"; \
    }; \
    git init -q /src; \
    git -C /src remote add origin https://github.com/FFmpeg/FFmpeg.git; \
    retry git -C /src fetch -q --depth 1 origin "${FFMPEG_COMMIT}"; \
    git -C /src checkout -q --detach FETCH_HEAD; \
    head=$(git -C /src rev-parse HEAD); \
    test "${head}" = "${FFMPEG_COMMIT}"; \
    retry git -C /src ls-remote origin \
        "refs/tags/${FFMPEG_VERSION}" "refs/tags/${FFMPEG_VERSION}^{}" >/tmp/tag; \
    tagged=$(awk -v tag="refs/tags/${FFMPEG_VERSION}" \
        '$2 == tag "^{}" { peeled = $1 } $2 == tag { plain = $1 } END { print (peeled != "" ? peeled : plain) }' /tmp/tag); \
    if [[ "${tagged}" != "${FFMPEG_COMMIT}" ]]; then \
        echo "tag ${FFMPEG_VERSION} names '${tagged}', the Containerfile pins ${FFMPEG_COMMIT}" >&2; \
        exit 1; \
    fi; \
    rm -rf /src/.git
WORKDIR /src
# Static, everything off, then only the formats Lidarr's plugins are likely
# to meet: what downloads from YouTube, Deezer, Tidal, Qobuz and Soulseek
# come as, and the cover art beside them. Decoders and demuxers are where
# FFmpeg's vulnerabilities are found, and the scan cannot see what a static
# build links in, so each format left out is one a crafted download cannot
# reach. Decoders: MPEG audio layers 1 to 3, AAC and LATM, Opus, Vorbis,
# WMA 1, 2 and Pro, AC-3, E-AC-3 and DTS; FLAC, ALAC, WavPack, Monkey's
# Audio, TrueHD, MLP and WMA Lossless; every PCM FFmpeg decodes natively,
# never AudioToolbox's, which only macOS has; and the cover art formats,
# JPEG, PNG, BMP, GIF, WebP and TIFF, WebP's decoder bringing in VP8's,
# since a lossy WebP image is a VP8 frame. Demuxers: the containers those
# come in, MP3, raw AAC and LATM, FLAC, Ogg, MP4, Matroska and WebM, WAV,
# W64, AIFF, CAF, ASF, Monkey's Audio, WavPack, raw AC-3, E-AC-3, DTS,
# TrueHD and MLP, and the image demuxers for the art. Parsers: those of the
# audio and art codecs above that have one. Left out: ADPCM, DSD, Musepack,
# RealAudio, ATRAC, Speex, WMA Voice, telephony and game audio, and all
# video but WebP's VP8, none of which a plugin downloads music as, so a
# file in any of them is refused rather than decoded. Encoders: LAME's MP3,
# libopus and libvorbis, and FFmpeg's own AAC, FLAC and ALAC, which the
# plugins transcode to, and MJPEG, to re-encode cover art. Muxers: only the
# files those encoders write, MP3, ADTS, M4A (ipod), MP4, Ogg, Opus, FLAC,
# and image2 for the art; MP4's and M4A's are FFmpeg's MOV muxer under
# other names, and bring it in. None for WAV, AIFF, AC-3, E-AC-3, ASF,
# Matroska or WebM: nothing here encodes PCM, AC-3 or WMA, and the plugins
# write none of them. One bitstream filter, aac_adtstoasc, which MP4 and
# M4A need for AAC copied from an ADTS stream; the MOV muxer selects it
# too, with VP9's superframe filter, which no audio needs. The two filters
# the plugins ask for. Every list names each component, since a glob such
# as pcm_* would take AudioToolbox's too. No network, no devices, nothing
# found by autodetection, so nothing links but what is named; no
# --enable-gpl, so the build is LGPL-2.1-or-later. nasm is not in DHI's
# repository, so amd64 builds without FFmpeg's hand-written x86 assembly;
# arm64's needs none. --toolchain=hardened is FFmpeg's own set of exploit
# mitigations, which Debian's build flags would otherwise give and a plain
# configure never reads: FORTIFY_SOURCE, a stack protector on every
# function, and position-independent binaries bound at load, full RELRO;
# the check below holds the last two. A component configure cannot build,
# one a new FFmpeg renamed, dropped or gated, it leaves out with no more
# than a warning, and the format would vanish without a word: any warning
# fails the build here, naming it. Each component goes to configure as an
# option of its own, since configure warns of a name it does not know only
# when nothing else in its list matches: one name in a list of many would
# be dropped with no warning at all.
RUN case "${TARGETARCH}" in \
        amd64) asm=--disable-x86asm ;; \
        arm64) asm= ;; \
        *) echo "no FFmpeg build for ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    lossy=mp1,mp1float,mp2,mp2float,mp3,mp3float,aac,aac_fixed,aac_latm,opus,vorbis,wmav1,wmav2,wmapro,ac3,ac3_fixed,eac3,dca; \
    lossless=flac,alac,wavpack,ape,truehd,mlp,wmalossless; \
    pcm=pcm_alaw,pcm_bluray,pcm_dvd,pcm_f16le,pcm_f24le,pcm_f32be,pcm_f32le,pcm_f64be,pcm_f64le,pcm_lxf,pcm_mulaw,pcm_s16be,pcm_s16be_planar,pcm_s16le,pcm_s16le_planar,pcm_s24be,pcm_s24daud,pcm_s24le; \
    pcm+=,pcm_s24le_planar,pcm_s32be,pcm_s32le,pcm_s32le_planar,pcm_s64be,pcm_s64le,pcm_s8,pcm_s8_planar,pcm_sga,pcm_u16be,pcm_u16le,pcm_u24be,pcm_u24le,pcm_u32be,pcm_u32le,pcm_u8,pcm_vidc; \
    art=mjpeg,png,bmp,gif,webp,tiff; \
    demuxers=mp3,aac,loas,flac,ogg,mov,matroska,wav,w64,aiff,caf,asf,ape,wv,ac3,eac3,dts,truehd,mlp; \
    demuxers+=,image2,image2pipe,image_jpeg_pipe,image_png_pipe,image_bmp_pipe,image_gif_pipe,image_webp_pipe,image_tiff_pipe; \
    opts=(); \
    enable() { \
        local names name; \
        IFS=, read -ra names <<<"$2"; \
        for name in "${names[@]}"; do opts+=("--enable-$1=${name}"); done; \
    }; \
    enable protocol file,pipe; \
    enable decoder "${lossy},${lossless},${pcm},${art}"; \
    enable encoder libmp3lame,aac,libopus,libvorbis,flac,alac,mjpeg; \
    enable demuxer "${demuxers}"; \
    enable parser aac,aac_latm,ac3,dca,flac,mlp,mpegaudio,opus,vorbis,mjpeg,png,bmp,gif,webp; \
    enable bsf aac_adtstoasc; \
    enable muxer mp3,adts,ipod,mp4,ogg,opus,flac,image2; \
    enable filter aresample,scale; \
    ./configure \
        --disable-everything --disable-autodetect --disable-network --disable-avdevice \
        --disable-programs --enable-ffmpeg --enable-ffprobe --disable-doc --disable-debug \
        --enable-static --disable-shared --pkg-config-flags=--static \
        --enable-zlib --enable-libmp3lame --enable-libopus --enable-libvorbis \
        "${opts[@]}" \
        --toolchain=hardened \
        ${asm:+"${asm}"} \
        | tee /tmp/configure.log; \
    if grep -n '^WARNING' /tmp/configure.log >&2; then \
        echo "ERROR: configure warned, above: a component it names is not built. Fix the Containerfile's lists for this FFmpeg." >&2; \
        exit 1; \
    fi
# make writes ffmpeg and ffprobe stripped, beside the _g builds it keeps
# symbols in. The notices of what they carry go beside them in the image:
# FFmpeg's licence, and the copyright files of the codec libraries linked
# in, LAME's under the LGPL, and Opus's, Vorbis's and Ogg's under the BSD
# licence, which asks that they ship with the binary.
RUN make -j"$(nproc)" \
    && install -d /out /notices \
    && install -m 0755 -t /out ffmpeg ffprobe \
    && install -m 0644 -t /notices LICENSE.md COPYING.LGPLv2.1 \
    && for pkg in libmp3lame0 libogg0 libopus0 libvorbis0a libvorbisenc2; do \
        install -m 0644 "/usr/share/doc/${pkg}/copyright" "/notices/${pkg}.copyright"; \
    done
# The SBOM of what no image scan can see: ffmpeg and ffprobe, built
# here, by FFmpeg's CPE at the release FFMPEG_VERSION names, and the four
# codec libraries linked into them, each by the package URL of the DHI
# build the stage installed, at the version dpkg reports, so it cannot go
# stale. ci's divbast-scan reads it through the label below, and judges
# each as it judges every package.
RUN ffmpeg_version=${FFMPEG_VERSION#n}; \
    components=$(printf '{"type":"application","name":"ffmpeg","version":"%s","cpe":"cpe:2.3:a:ffmpeg:ffmpeg:%s:*:*:*:*:*:*:*","licenses":[{"license":{"id":"LGPL-2.1-or-later"}}]}' \
        "${ffmpeg_version}" "${ffmpeg_version}"); \
    while read -r pkg version source; do \
        encoded=${version//+/%2B}; \
        encoded=${encoded//:/%3A}; \
        components+=$(printf ',{"type":"library","name":"%s","version":"%s","purl":"pkg:deb/dhi/%s@%s?arch=%s&distro=dhi-13&upstream=%s"}' \
            "${pkg}" "${version}" "${pkg}" "${encoded}" "${TARGETARCH}" "${source}"); \
    done < <(dpkg-query -W -f '${Package} ${Version} ${source:Package}\n' libmp3lame-dev libogg-dev libopus-dev libvorbis-dev); \
    install -d /sbom; \
    printf '{"bomFormat":"CycloneDX","specVersion":"1.6","version":1,"components":[%s]}\n' "${components}" >/sbom/sbom.cdx.json; \
    test "$(grep -o '"name":' /sbom/sbom.cdx.json | wc -l)" -eq 5; \
    cat /sbom/sbom.cdx.json
COPY test/ffmpeg-check.sh /tmp/ffmpeg-check.sh
# The check reads /out, which the runtime stage copies both binaries
# from, once nothing more is written there, so what it passes is what
# ships: each answers as itself, is position-independent and bound at
# load, ffmpeg has the three library encoders, and neither needs a shared
# library the runtime base lacks.
RUN /tmp/ffmpeg-check.sh /out

FROM ${RUNTIME_IMAGE}
ARG LIDARR_VERSION

# Root-owned, and never written: Lidarr writes to /config and TMPDIR alone
COPY --from=backend /out/bin /app/bin
COPY --from=frontend /src/_output/UI /app/bin/UI
COPY --from=backend /out/package_info /app/package_info
# Lidarr's licence, where upstream's release tarball puts it: its packaging
# copies it into the tarball's Lidarr/ folder, which is this /app/bin
COPY --from=fetch /src/LICENSE.md /app/bin/LICENSE.md
COPY --from=probe /out/divbast-lidarr /usr/local/bin/divbast-lidarr
# On the base's PATH, for the plugins that look for them there
COPY --from=ffmpeg /out/ffmpeg /out/ffprobe /usr/local/bin/
COPY --from=ffmpeg /notices /usr/local/share/doc/ffmpeg
COPY --from=ffmpeg /sbom/sbom.cdx.json /usr/local/share/divbast/sbom.cdx.json
# The data directory, empty and uid 65532's, and not a VOLUME: a new named
# volume mounted here takes its owner from it, so Lidarr can write to it.
COPY --from=backend --chown=65532:65532 /out/config /config
# Lidarr takes ASP.NET Core's content root from its working directory, and
# sets none of its own. It loads no appsettings.json and watches only the
# music root folders it is given, so / would do as well, but the content
# root is then a folder Lidarr's own files are in, never the whole
# filesystem.
WORKDIR /app/bin

# Any config.xml key can be set as LIDARR__<SECTION>__<KEY>. The port is
# 8080, as every divbast image's. The runtime opens no diagnostics socket.
# PGGSSENCMODE stops Npgsql probing for GSS encryption, which matters only
# for PostgreSQL. Analytics and authentication stay as upstream ships them:
# analytics on, and authentication off until a method is set, which the
# README says to do first.
ENV LIDARR__SERVER__PORT=8080 \
    DOTNET_EnableDiagnostics=0 \
    PGGSSENCMODE=disable \
    TMPDIR=/tmp

LABEL org.opencontainers.image.source="https://github.com/divbast/lidarr-oci-image" \
    org.opencontainers.image.url="https://github.com/divbast/lidarr-oci-image" \
    org.opencontainers.image.title="Lidarr" \
    org.opencontainers.image.version="${LIDARR_VERSION}" \
    org.opencontainers.image.licenses="GPL-3.0-only" \
    io.github.divbast.scan.sboms="/usr/local/share/divbast/sbom.cdx.json"

EXPOSE 8080
# Exec form: the shell form would need /bin/sh, which the image lacks.
# --timeout must clear divbast-lidarr health's own budget: it allows each
# of its two exchanges 4 s and runs them one after the other, so a listener
# that accepts and never answers costs 8 s. A shorter timeout kills the
# probe mid-flight and records a bare engine timeout instead of the probe's
# own line, which is the only diagnostic there is. Podman reads no
# HEALTHCHECK from an image published as an OCI index: its users pass the
# same array as --health-cmd. Lidarr listens only once its database
# migrations finish, and a nightly may add migrations a large library
# takes a while over: migrations that outlast the start period and three
# failed checks get the container marked unhealthy, and autoheal or a Swarm
# service would then kill it mid-migration. The first check that passes
# ends the start period, so a long one costs a healthy start nothing; it
# only delays the verdict on a start that never comes up. Hence 300 s. The
# contract suite's Healthcheck says the same, and must.
HEALTHCHECK --interval=30s --timeout=10s --start-period=300s --retries=3 \
    CMD ["/usr/local/bin/divbast-lidarr", "health"]
USER 65532:65532
# Lidarr itself runs as PID 1, with nothing before it. .NET handles SIGTERM
# there and reaps its child processes, fpcalc among them, so it needs no
# init; a restart from the UI happens inside the process. The apphost finds
# the runtime through the base's DOTNET_ROOT. -exitimmediately is
# upstream's own flag, read in one place: a startup failure upstream calls
# non-recoverable, such as a corrupt config.xml, then exits 3 at once,
# where without it Lidarr would wait an hour for a key that no one can
# press in a container, and the container would sit there looking alive.
ENTRYPOINT ["/app/bin/Lidarr", "-nobrowser", "-exitimmediately", "-data=/config"]
