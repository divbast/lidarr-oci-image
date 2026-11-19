#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

# Usage: ffmpeg-check.sh BIN_DIR
#
# Checks the ffmpeg and ffprobe the Containerfile's ffmpeg stage built
# into BIN_DIR are the ones the runtime can run and Lidarr's plugins can
# use. The stage runs it as its last step, on the folder the runtime stage
# copies both from, so what it passes is what ships.
#
# What it guards: each binary answers -version as itself; ffmpeg lists the
# libmp3lame, libopus and libvorbis encoders, which the plugins transcode
# to MP3, Opus and Vorbis with, and which a configure line or a missing
# library loses without failing the build; and neither needs a shared
# library but glibc's libc and libm, zlib's libz and the loader, the ones
# the runtime base carries. The codec libraries must link in statically:
# one linked dynamically would build and pass every test here, and then
# fail to start in the image, which carries none of them.
#
# readelf reads each binary's dynamic section, whose NEEDED entries are
# the libraries it asks the loader for. -encoders prints a legend, a line
# of dashes, then an encoder a line with its name the second field: only
# names below the dashes count, and only as that field, so an encoder
# named in another's description does not.
#
# test/ffmpeg runs it over fixtures, and checks each way it can refuse.

set -euo pipefail

if (($# != 1)) || [[ ! -d $1 ]]; then
	echo "usage: ffmpeg-check.sh BIN_DIR" >&2
	exit 2
fi
dir=$1

fail() {
	echo "ERROR: $*" >&2
	exit 1
}

for name in ffmpeg ffprobe; do
	bin=${dir}/${name}
	if [[ ! -f ${bin} || ! -x ${bin} ]]; then
		fail "${bin} is missing or not executable."
	fi

	code=0
	version=$("${bin}" -version) || code=$?
	if ((code != 0)); then
		fail "${bin} -version failed, exit ${code}."
	fi
	if [[ ${version} != "${name} version "* ]]; then
		fail "${bin} -version does not say ${name} version: ${version%%$'\n'*}"
	fi

	code=0
	section=$(readelf -d "${bin}" 2>&1) || code=$?
	if ((code != 0)); then
		fail "readelf cannot read ${bin}: ${section}"
	fi
	needed=$(sed -n 's/^.*(NEEDED).*Shared library: \[\(.*\)\]$/\1/p' <<<"${section}")
	while read -r lib; do
		case ${lib} in
		'' | libc.so.6 | libm.so.6 | libz.so.1 | ld-linux-x86-64.so.2 | ld-linux-aarch64.so.1) ;;
		*) fail "${bin} needs ${lib}, which the runtime base does not carry: only libc, libm, libz and the loader come from it, and the codec libraries must link in statically." ;;
		esac
	done <<<"${needed}"
done

code=0
listed=$("${dir}/ffmpeg" -hide_banner -encoders) || code=$?
if ((code != 0)); then
	fail "${dir}/ffmpeg -encoders failed, exit ${code}."
fi
for encoder in libmp3lame libopus libvorbis; do
	if ! awk -v encoder="${encoder}" '
		below && $2 == encoder { found = 1 }
		$1 ~ /^-+$/ { below = 1 }
		END { exit !found }
	' <<<"${listed}"; then
		fail "ffmpeg has no ${encoder} encoder, which Lidarr's plugins transcode with: configure must enable it, with its library installed."
	fi
done

echo "ffmpeg-check: OK: ffmpeg and ffprobe answer -version, ffmpeg has the libmp3lame, libopus and libvorbis encoders, and neither needs a shared library but libc, libm, libz and the loader."
