// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/divbast/ci/harness"
)

// fpcalc is the binary Lidarr runs to fingerprint audio files for
// AcoustID, from the folder it runs from. It comes from NuGet, static,
// without its execute bit, which the backend stage gives back.
const fpcalc = "/app/bin/fpcalc"

// healthCheck is an entry of Lidarr's Health page, as the API lists it.
type healthCheck struct {
	Source  string `json:"source"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

// TestFpcalcRuns runs fpcalc in a serving container, as Lidarr's own user
// under the contract's confinement, as Lidarr runs it on an import: it
// must start, which takes its execute bit and a binary that needs nothing
// the base lacks, and say what it is. Lidarr checks it too, on starting,
// and a Health page entry naming fpcalc means it found it missing, not
// executable or too old: there must be none.
func TestFpcalcRuns(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	s := serveFresh(t, e, image)
	checks := harness.NewChecks(t)

	res, err := s.C.Exec(t.Context(), nil, fpcalc, "-version")
	if err != nil {
		checks.True(fpcalc+" -version runs", false, err.Error())
	} else {
		out := string(res.Stdout)
		checks.True(fpcalc+" -version exits 0", res.Code == 0,
			fmt.Sprintf("exit status %d, printing %s", res.Code, harness.Clip(out+string(res.Stderr))))
		checks.True(fpcalc+" -version says it is fpcalc", strings.HasPrefix(out, "fpcalc version "), harness.Clip(out))
	}

	if err := logged(t.Context(), s.C, hostStarted, harness.ServeWithin); err != nil {
		checks.True("Lidarr's host finishes starting, its health checks run", false, err.Error())
		return
	}
	health, ok := callAPI[[]healthCheck](t, s, "GET /api/v1/health answers 200", http.StatusOK,
		http.MethodGet, "/api/v1/health", nil)
	if !ok {
		return
	}
	var found []string
	for _, h := range health {
		if strings.Contains(strings.ToLower(h.Source+" "+h.Message), "fpcalc") {
			found = append(found, h.Source+": "+h.Message)
		}
	}
	checks.True("the Health page has no entry about fpcalc", len(found) == 0, fmt.Sprintf("%q", found))
}
