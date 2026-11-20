// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// Package stable holds the image on Lidarr's v4 nightly line, and says
// when that has to end: once upstream publishes v4 stable, the image moves
// to it by hand, as CLAUDE.md's "Moving to stable" says.
package stable

import (
	"os"
	"regexp"
	"testing"
)

// none is the watch's value while Lidarr has no stable v4 release: a
// version no release has. It must share no major, minor and patch with a
// v4 nightly, every one of which is 4.0.0.N, because Renovate offers a
// pre-release that shares all three with the current version, stable
// flag or not.
const none = "0.0.0.0"

// watch finds the Containerfile's line Renovate writes Lidarr's first
// stable v4 release to. It lives in the Containerfile, beside the pins,
// because Renovate's recommended preset ignores every path under test/.
var watch = regexp.MustCompile(`(?m)^# LIDARR_STABLE_V4=(\S*)$`)

// TestNoV4StableYet fails once Renovate has written a stable v4 release
// into the watch, naming the steps that move the image to it. Renovate's
// entry for the watch shares a group, and so a branch and a pull request,
// with the nightly line's pins, and never merges itself: once this fails
// that pull request, every nightly after it lands there too, so the image
// stays on the nightly it has. It fails, too, on a Containerfile that has
// lost the watch, or has two, since then nothing would see v4 stable
// arrive.
func TestNoV4StableYet(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("../../Containerfile")
	if err != nil {
		t.Fatal(err)
	}
	found := watch.FindAllSubmatch(b, -1)
	if len(found) != 1 {
		t.Fatalf("the Containerfile has %d '# LIDARR_STABLE_V4=' lines, want 1: Renovate writes Lidarr's first stable v4 release there", len(found))
	}
	if v := string(found[0][1]); v != none {
		t.Fatalf("Lidarr v4 is stable: release %s. The nightly line stops here until the image moves to stable: CLAUDE.md, 'Moving to stable'.", v)
	}
}
