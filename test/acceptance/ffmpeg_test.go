// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/divbast/ci/harness"
)

// tone is the fixture the transcodes start from: a second of a 440 Hz
// sine, 44.1 kHz stereo FLAC, which the image's own ffmpeg made.
const tone = "testdata/tone.flac"

// TestFFmpegTranscodes runs ffmpeg and ffprobe in a serving container, as
// Lidarr's own user under the contract's confinement, as a plugin runs
// them: each found by name on PATH, as plugins look for them, ffmpeg
// transcodes the fixture, fed on its standard input, to MP3, Opus, Vorbis
// and AAC in /tmp, the one place the container may write outside
// /config, and ffprobe reads each back as the codec asked for. MP3, Opus
// and Vorbis come from the codec libraries linked into ffmpeg, which the
// runtime base does not carry, so a library left shared would fail here.
func TestFFmpegTranscodes(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	s := serveFresh(t, e, image)
	flac, err := os.ReadFile(tone)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ encoder, out, codec string }{
		{encoder: "libmp3lame", out: "/tmp/tone.mp3", codec: "mp3"},
		{encoder: "libopus", out: "/tmp/tone.opus", codec: "opus"},
		{encoder: "libvorbis", out: "/tmp/tone.ogg", codec: "vorbis"},
		{encoder: "aac", out: "/tmp/tone.m4a", codec: "aac"},
	} {
		checks := harness.NewChecks(t)
		res, err := s.C.Exec(t.Context(), bytes.NewReader(flac),
			"ffmpeg", "-hide_banner", "-loglevel", "error", "-i", "pipe:0", "-c:a", tc.encoder, "-y", tc.out)
		if err != nil {
			checks.True("ffmpeg, found on PATH, transcodes the fixture with "+tc.encoder, false, err.Error())
			continue
		}
		checks.True("ffmpeg, found on PATH, transcodes the fixture with "+tc.encoder+" to "+tc.out, res.Code == 0,
			fmt.Sprintf("exit status %d, printing %s", res.Code, harness.Clip(string(res.Stdout)+string(res.Stderr))))
		if res.Code != 0 {
			continue
		}
		res, err = s.C.Exec(t.Context(), nil,
			"ffprobe", "-v", "error", "-print_format", "json", "-show_streams", tc.out)
		if err != nil {
			checks.True("ffprobe, found on PATH, reads "+tc.out, false, err.Error())
			continue
		}
		var probed struct {
			Streams []struct {
				CodecName string `json:"codec_name"`
				CodecType string `json:"codec_type"`
			} `json:"streams"`
		}
		if res.Code != 0 || json.Unmarshal(res.Stdout, &probed) != nil {
			checks.True("ffprobe, found on PATH, reads "+tc.out+" as JSON", false,
				fmt.Sprintf("exit status %d, printing %s", res.Code, harness.Clip(string(res.Stdout)+string(res.Stderr))))
			continue
		}
		codecs := make([]string, 0, len(probed.Streams))
		for _, st := range probed.Streams {
			codecs = append(codecs, st.CodecType+"/"+st.CodecName)
		}
		harness.Equal(checks, "ffprobe reads "+tc.out+" as one audio stream of "+tc.codec, "audio/"+tc.codec, strings.Join(codecs, ","))
	}
}
