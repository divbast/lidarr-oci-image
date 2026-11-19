// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// Package ffmpeg tests test/ffmpeg-check.sh, the check the Containerfile's
// ffmpeg stage runs over the ffmpeg and ffprobe it builds. The script runs
// against fixtures: fake binaries that print what a test chooses for
// -version and -encoders, and a fake readelf on PATH that prints, for a
// binary, the dynamic section written beside it, in GNU readelf's own
// format. The real readelf, on real binaries, runs in the stage. The good
// set must pass, on either architecture's loader, and each way the check
// can refuse is seen refusing: an encoder missing, or named only in
// another's description, a shared library the runtime base does not
// carry, a binary missing, not executable, not answering, or not an ELF
// file. The Containerfile must run it where it must, and the check of that
// is seen refusing a Containerfile changed each way it could be wired
// wrong.
package ffmpeg

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// script is the script under test, from this package's directory.
const script = "../ffmpeg-check.sh"

// ok is the line the script prints when both binaries pass.
const ok = "ffmpeg-check: OK"

// encoders is what ffmpeg -hide_banner -encoders prints for the build the
// stage configures, cut to a few of its lines: the legend, the marker and
// then one encoder a line, its name the second field.
const encoders = ` V..... = Video
 A..... = Audio
 S..... = Subtitle
 .F.... = Frame-level multithreading
 ..S... = Slice-level multithreading
 ...X.. = Codec is experimental
 ....B. = Supports draw_horiz_band
 .....D = Supports direct rendering method 1
 ------
 V....D mjpeg                MJPEG (Motion JPEG)
 A....D aac                  AAC (Advanced Audio Coding)
 A....D flac                 FLAC (Free Lossless Audio Codec)
 A....D libmp3lame           libmp3lame MP3 (MPEG audio layer 3) (codec mp3)
 A....D libopus              libopus Opus (codec opus)
 A....D libvorbis            libvorbis (codec vorbis)
`

// dynamic is readelf -d's report of a binary needing the given libraries,
// with the other entries a real one has around them.
func dynamic(needed ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nDynamic section at offset 0x1fd28 contains %d entries:\n", len(needed)+3)
	b.WriteString("  Tag        Type                         Name/Value\n")
	for _, lib := range needed {
		fmt.Fprintf(&b, " 0x0000000000000001 (NEEDED)             Shared library: [%s]\n", lib)
	}
	b.WriteString(" 0x000000000000000c (INIT)               0x4b0e0\n")
	b.WriteString(" 0x000000000000001d (RUNPATH)            Library runpath: [/usr/lib]\n")
	b.WriteString(" 0x0000000000000000 (NULL)               0x0\n")
	return b.String()
}

// The loaders of the two architectures the image is built for.
const (
	amd64Loader = "ld-linux-x86-64.so.2"
	arm64Loader = "ld-linux-aarch64.so.1"
)

// binary is one fake program: what -version and -encoders print and how
// each exits, and the dynamic section readelf reports for it, none
// meaning readelf finds no ELF file. absent leaves it out of the folder,
// and noexec writes it without its execute bit.
type binary struct {
	version      string
	versionCode  int
	encoders     string
	encodersCode int
	dynamic      string
	absent       bool
	noexec       bool
}

// good returns the program name as the stage builds it, on the given
// loader.
func good(name, loader string) binary {
	return binary{
		version:  name + " version n9.0.2 Copyright (c) 2000-2025 the FFmpeg developers\nbuilt with gcc 14.2.0\n",
		encoders: encoders,
		dynamic:  dynamic("libm.so.6", "libz.so.1", "libc.so.6", loader),
	}
}

// fakeProgram is a shell script that answers -version and -hide_banner
// -encoders as b says, and refuses anything else, so a check asking
// something new shows here.
func fakeProgram(b binary) string {
	return fmt.Sprintf(`#!/bin/sh
if [ "$*" = "-version" ]; then
cat <<'EOF'
%sEOF
exit %d
fi
if [ "$*" = "-hide_banner -encoders" ]; then
cat <<'EOF'
%sEOF
exit %d
fi
echo "fake $0: unexpected arguments: $*" >&2
exit 64
`, b.version, b.versionCode, b.encoders, b.encodersCode)
}

// fakeReadelf prints the dynamic section written beside the file it is
// asked about, and fails as GNU readelf does on a file that is not ELF
// when none is there.
const fakeReadelf = `#!/bin/sh
if [ "$#" -ne 2 ] || [ "$1" != -d ]; then
	echo "fake readelf: unexpected arguments: $*" >&2
	exit 64
fi
if [ -f "$2.dynamic" ]; then
	cat "$2.dynamic"
	exit 0
fi
echo "readelf: Error: Not an ELF file - it has the wrong magic bytes at the start" >&2
exit 1
`

// result is how a run of the script ended.
type result struct {
	code           int
	stdout, stderr string
}

// write makes a folder holding ffmpeg and ffprobe as given, and a folder
// holding the fake readelf, and returns both.
func write(t *testing.T, ffmpeg, ffprobe binary) (bin, tools string) {
	t.Helper()

	bin, tools = t.TempDir(), t.TempDir()
	for name, b := range map[string]binary{"ffmpeg": ffmpeg, "ffprobe": ffprobe} {
		if b.absent {
			continue
		}
		mode := os.FileMode(0o755)
		if b.noexec {
			mode = 0o644
		}
		program := filepath.Join(bin, name)
		if err := os.WriteFile(program, []byte(fakeProgram(b)), mode); err != nil {
			t.Fatal(err)
		}
		if b.dynamic != "" {
			if err := os.WriteFile(program+".dynamic", []byte(b.dynamic), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(tools, "readelf"), []byte(fakeReadelf), 0o755); err != nil { //nolint:gosec // G306: the fake readelf must be executable to stand in for the real one
		t.Fatal(err)
	}
	return bin, tools
}

// run runs the script with args, the fake readelf first on PATH.
func run(t *testing.T, tools string, args ...string) result {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), script, args...) //nolint:gosec // G204: the script is this repository's own, run on fixtures the test made
	cmd.Env = []string{"PATH=" + tools + string(os.PathListSeparator) + os.Getenv("PATH")}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			t.Fatalf("%s: %v", script, err)
		}
	}
	return result{cmd.ProcessState.ExitCode(), stdout.String(), stderr.String()}
}

// TestPasses checks the build the stage makes passes, on either
// architecture's loader, and on one needing fewer libraries.
func TestPasses(t *testing.T) {
	t.Parallel()

	fewer := good("ffprobe", arm64Loader)
	fewer.dynamic = dynamic("libc.so.6", arm64Loader)
	tests := []struct {
		name            string
		ffmpeg, ffprobe binary
	}{
		{name: "amd64", ffmpeg: good("ffmpeg", amd64Loader), ffprobe: good("ffprobe", amd64Loader)},
		{name: "arm64", ffmpeg: good("ffmpeg", arm64Loader), ffprobe: good("ffprobe", arm64Loader)},
		{name: "fewer-libraries", ffmpeg: good("ffmpeg", arm64Loader), ffprobe: fewer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bin, tools := write(t, tt.ffmpeg, tt.ffprobe)
			got := run(t, tools, bin)
			if got.code != 0 || !strings.HasPrefix(got.stdout, ok) || got.stderr != "" {
				t.Fatalf("got exit %d, stdout %q, stderr %q; want exit 0, %q first, nothing on stderr", got.code, got.stdout, got.stderr, ok)
			}
		})
	}
}

// TestRefuses checks each way the binaries can be wrong fails the check
// with a line naming what is wrong, and never prints the OK line.
func TestRefuses(t *testing.T) {
	t.Parallel()

	without := func(enc string) binary {
		b := good("ffmpeg", amd64Loader)
		var kept []string
		for line := range strings.Lines(b.encoders) {
			if f := strings.Fields(line); len(f) < 2 || f[1] != enc {
				kept = append(kept, line)
			}
		}
		b.encoders = strings.Join(kept, "")
		return b
	}
	tests := []struct {
		name            string
		ffmpeg, ffprobe binary
		want            string
	}{
		{name: "no-libmp3lame", ffmpeg: without("libmp3lame"), want: "ffmpeg has no libmp3lame encoder"},
		{name: "no-libopus", ffmpeg: without("libopus"), want: "ffmpeg has no libopus encoder"},
		{name: "no-libvorbis", ffmpeg: without("libvorbis"), want: "ffmpeg has no libvorbis encoder"},
		{
			// FFmpeg's own Vorbis encoder, whose description names the
			// library it is not
			name: "libvorbis-only-in-a-description",
			ffmpeg: func() binary {
				b := without("libvorbis")
				b.encoders += " A...X. vorbis               Vorbis (unlike libvorbis, experimental)\n"
				return b
			}(),
			want: "ffmpeg has no libvorbis encoder",
		},
		{
			// An encoder named only in the legend, above the marker
			name: "libopus-only-above-the-marker",
			ffmpeg: func() binary {
				b := without("libopus")
				b.encoders = " A..... libopus\n" + b.encoders
				return b
			}(),
			want: "ffmpeg has no libopus encoder",
		},
		{
			name: "encoders-fails",
			ffmpeg: func() binary {
				b := good("ffmpeg", amd64Loader)
				b.encodersCode = 1
				return b
			}(),
			want: "ffmpeg -encoders failed, exit 1",
		},
		{
			name: "ffmpeg-needs-libmp3lame",
			ffmpeg: func() binary {
				b := good("ffmpeg", amd64Loader)
				b.dynamic = dynamic("libmp3lame.so.0", "libm.so.6", "libc.so.6", amd64Loader)
				return b
			}(),
			want: "/ffmpeg needs libmp3lame.so.0",
		},
		{
			name: "ffprobe-needs-libvorbis",
			ffprobe: func() binary {
				b := good("ffprobe", amd64Loader)
				b.dynamic = dynamic("libc.so.6", "libvorbis.so.0a", amd64Loader)
				return b
			}(),
			want: "/ffprobe needs libvorbis.so.0a",
		},
		{
			// A library whose name only starts like an allowed one
			name: "libz-lookalike",
			ffmpeg: func() binary {
				b := good("ffmpeg", amd64Loader)
				b.dynamic = dynamic("libc.so.6", "libzstd.so.1", amd64Loader)
				return b
			}(),
			want: "/ffmpeg needs libzstd.so.1",
		},
		{
			name: "not-elf",
			ffmpeg: func() binary {
				b := good("ffmpeg", amd64Loader)
				b.dynamic = ""
				return b
			}(),
			want: "readelf cannot read",
		},
		{name: "ffmpeg-absent", ffmpeg: binary{absent: true}, want: "/ffmpeg is missing or not executable"},
		{name: "ffprobe-absent", ffprobe: binary{absent: true}, want: "/ffprobe is missing or not executable"},
		{
			name: "ffprobe-not-executable",
			ffprobe: func() binary {
				b := good("ffprobe", amd64Loader)
				b.noexec = true
				return b
			}(),
			want: "/ffprobe is missing or not executable",
		},
		{
			name: "version-fails",
			ffprobe: func() binary {
				b := good("ffprobe", amd64Loader)
				b.versionCode = 1
				return b
			}(),
			want: "/ffprobe -version failed, exit 1",
		},
		{
			// ffmpeg where ffprobe should be: it answers, as the wrong program
			name: "ffprobe-is-ffmpeg",
			ffprobe: func() binary {
				b := good("ffmpeg", amd64Loader)
				return b
			}(),
			want: "/ffprobe -version does not say ffprobe version",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ffmpeg, ffprobe := tt.ffmpeg, tt.ffprobe
			if ffmpeg == (binary{}) {
				ffmpeg = good("ffmpeg", amd64Loader)
			}
			if ffprobe == (binary{}) {
				ffprobe = good("ffprobe", amd64Loader)
			}
			bin, tools := write(t, ffmpeg, ffprobe)
			got := run(t, tools, bin)
			if got.code != 1 || !strings.Contains(got.stderr, "ERROR: ") || !strings.Contains(got.stderr, tt.want) || strings.Contains(got.stdout, ok) {
				t.Fatalf("got exit %d, stdout %q, stderr %q; want exit 1, an ERROR line naming %q, and no OK line", got.code, got.stdout, got.stderr, tt.want)
			}
		})
	}
}

// TestUsage checks the script refuses anything but one existing folder,
// with its usage.
func TestUsage(t *testing.T) {
	t.Parallel()

	bin, tools := write(t, good("ffmpeg", amd64Loader), good("ffprobe", amd64Loader))
	tests := []struct {
		name string
		args []string
	}{
		{name: "none"},
		{name: "two", args: []string{bin, bin}},
		{name: "not-a-folder", args: []string{filepath.Join(bin, "ffmpeg")}},
		{name: "absent", args: []string{filepath.Join(bin, "nowhere")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := run(t, tools, tt.args...)
			if got.code != 2 || !strings.Contains(got.stderr, "usage: ffmpeg-check.sh BIN_DIR") || strings.Contains(got.stdout, ok) {
				t.Fatalf("got exit %d, stdout %q, stderr %q; want exit 2 and the usage", got.code, got.stdout, got.stderr)
			}
		})
	}
}

// TestContainerfileRunsIt fails unless the Containerfile runs the check
// where it must: in the stage that builds ffmpeg and ffprobe, after make,
// as its last step, on the folder the runtime stage copies both from.
func TestContainerfileRunsIt(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	data, err := root.ReadFile("Containerfile")
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range wiringProblems(string(data)) {
		t.Error(problem)
	}
}

// wired is a Containerfile cut to what the wiring check reads, wired as
// it must be.
const wired = `# syntax and comments are skipped
FROM dhi.io/golang:1.27.4-debian13-dev AS ffmpeg
RUN ./configure --disable-everything \\
    --enable-libmp3lame
RUN make -j"$(nproc)" \\
    && install -m 0755 -t /out ffmpeg ffprobe
COPY test/ffmpeg-check.sh /tmp/ffmpeg-check.sh
# the check reads what ships
RUN /tmp/ffmpeg-check.sh /out
FROM dhi.io/golang:1.27.4-debian13-dev AS probe
RUN go build ./cmd/divbast-lidarr
FROM ${RUNTIME_IMAGE}
COPY --from=probe /out/divbast-lidarr /usr/local/bin/divbast-lidarr
COPY --from=ffmpeg /out/ffmpeg /out/ffprobe /usr/local/bin/
`

// TestWiringProblems checks the wiring check passes a Containerfile wired
// as it must be, and refuses each way one could be wired wrong.
func TestWiringProblems(t *testing.T) {
	t.Parallel()

	const (
		check   = "RUN /tmp/ffmpeg-check.sh /out\n"
		shipped = "COPY --from=ffmpeg /out/ffmpeg /out/ffprobe /usr/local/bin/\n"
		notCopy = "the runtime stage does not copy /out/ffmpeg and /out/ffprobe"
	)
	tests := []struct {
		name, old, new, want string
	}{
		{name: "wired"},
		{
			name: "folder-with-slash",
			old:  check, new: "RUN /tmp/ffmpeg-check.sh /out/\n",
		},
		{
			name: "no-copy",
			old:  "COPY test/ffmpeg-check.sh /tmp/ffmpeg-check.sh\n",
			want: "does not COPY test/ffmpeg-check.sh into a build stage",
		},
		{name: "never-run", old: check, want: "never runs it"},
		{
			name: "only-in-a-comment",
			old:  check, new: "# RUN /tmp/ffmpeg-check.sh /out\n",
			want: "never runs it",
		},
		{
			name: "before-make",
			old:  "RUN make",
			new:  "COPY test/ffmpeg-check.sh /tmp/ffmpeg-check.sh\nRUN /tmp/ffmpeg-check.sh /out\nRUN make",
			want: "before make builds",
		},
		{
			name: "more-after-the-check",
			old:  check, new: check + "RUN strip /out/ffmpeg\n",
			want: "runs more after the check",
		},
		{
			name: "copied-from-another-folder",
			old:  shipped, new: "COPY --from=ffmpeg /src/ffmpeg /src/ffprobe /usr/local/bin/\n",
			want: notCopy,
		},
		{
			name: "ffprobe-not-shipped",
			old:  shipped, new: "COPY --from=ffmpeg /out/ffmpeg /usr/local/bin/\n",
			want: notCopy,
		},
		{
			name: "copied-from-another-stage",
			old:  shipped, new: "COPY --from=probe /out/ffmpeg /out/ffprobe /usr/local/bin/\n",
			want: notCopy,
		},
		{
			name: "shipped-off-path",
			old:  shipped, new: "COPY --from=ffmpeg /out/ffmpeg /out/ffprobe /opt/ffmpeg/\n",
			want: notCopy,
		},
		{
			name: "shipped-by-an-earlier-stage",
			old:  shipped, new: "",
			want: notCopy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			containerfile := wired
			if tt.old != "" {
				if strings.Count(wired, tt.old) != 1 {
					t.Fatalf("%q is not in the fixture exactly once", tt.old)
				}
				containerfile = strings.Replace(wired, tt.old, tt.new, 1)
			}
			got := wiringProblems(containerfile)
			if tt.want == "" {
				if len(got) != 0 {
					t.Fatalf("got %q; want no problems", got)
				}
				return
			}
			if !slices.ContainsFunc(got, func(p string) bool { return strings.Contains(p, tt.want) }) {
				t.Fatalf("got %q; want a problem naming %q", got, tt.want)
			}
		})
	}
}

// instruction is one Containerfile instruction, its continuation lines
// joined, in the stage it belongs to.
type instruction struct {
	stage         int
	from, keyword string
	args          string
}

// instructions reads a Containerfile's instructions in order, skipping
// comments, which is all the wiring check needs. Each FROM starts a stage,
// named by its AS, if it has one.
func instructions(containerfile string) []instruction {
	var out []instruction
	stage, name := -1, ""
	var pending strings.Builder
	for line := range strings.Lines(containerfile) {
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if body, ok := strings.CutSuffix(line, "\\"); ok {
			pending.WriteString(body + " ")
			continue
		}
		pending.WriteString(line)
		text := strings.TrimSpace(pending.String())
		pending.Reset()
		keyword, args, _ := strings.Cut(text, " ")
		if keyword == "" {
			continue
		}
		keyword = strings.ToUpper(keyword)
		if keyword == "FROM" {
			stage++
			name = ""
			if f := strings.Fields(args); len(f) == 3 && strings.EqualFold(f[1], "AS") {
				name = f[2]
			}
		}
		out = append(out, instruction{stage: stage, from: name, keyword: keyword, args: strings.TrimSpace(args)})
	}
	return out
}

// wiringProblems says what is wrong with how containerfile runs the
// check, or nothing when it runs it where it must.
func wiringProblems(containerfile string) []string {
	all := instructions(containerfile)
	copied := slices.IndexFunc(all, func(in instruction) bool {
		f := strings.Fields(in.args)
		return in.keyword == "COPY" && len(f) >= 2 && slices.Contains(f[:len(f)-1], "test/ffmpeg-check.sh")
	})
	if copied < 0 {
		return []string{"the Containerfile does not COPY test/ffmpeg-check.sh into a build stage"}
	}
	stage := all[copied].stage
	name := all[copied].from
	dst := strings.Fields(all[copied].args)[len(strings.Fields(all[copied].args))-1]
	runs := regexp.MustCompile(`(?:^|[\s;&|])` + regexp.QuoteMeta(dst) + `\s+(\S+)`)
	checked, dir := -1, ""
	for i := copied + 1; i < len(all) && all[i].stage == stage; i++ {
		if m := runs.FindStringSubmatch(all[i].args); all[i].keyword == "RUN" && m != nil {
			checked, dir = i, path.Clean(m[1])
			break
		}
	}
	if checked < 0 {
		return []string{"the Containerfile copies the script to " + dst + " but never runs it after, in that stage"}
	}

	var problems []string
	makes := regexp.MustCompile(`(?:^|[\s;&|(])make(?:\s|$)`)
	built := false
	for i := checked - 1; i >= 0 && all[i].stage == stage; i-- {
		if all[i].keyword == "RUN" && makes.MatchString(all[i].args) {
			built = true
			break
		}
	}
	if !built {
		problems = append(problems, "the Containerfile runs the check before make builds the binaries it reads")
	}
	for i := checked + 1; i < len(all) && all[i].stage == stage; i++ {
		if all[i].keyword == "RUN" {
			problems = append(problems, "the Containerfile runs more after the check in its stage, so what ships may not be what it passed: "+all[i].args)
		}
	}

	last := all[len(all)-1].stage
	from := "--from=" + name
	shipped := map[string]bool{}
	for _, in := range all {
		f := strings.Fields(in.args)
		if in.stage != last || in.keyword != "COPY" || name == "" || !slices.Contains(f, from) {
			continue
		}
		if dest := strings.TrimSuffix(f[len(f)-1], "/"); dest != "/usr/local/bin" {
			continue
		}
		for _, src := range f[:len(f)-1] {
			shipped[path.Clean(src)] = true
		}
	}
	if !shipped[path.Join(dir, "ffmpeg")] || !shipped[path.Join(dir, "ffprobe")] {
		problems = append(problems, fmt.Sprintf("the runtime stage does not copy %s and %s from the %s stage, where the check read them, to /usr/local/bin/", path.Join(dir, "ffmpeg"), path.Join(dir, "ffprobe"), name))
	}
	return problems
}
