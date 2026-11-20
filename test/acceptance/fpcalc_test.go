// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

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
// executable or too old: there must be none. Lidarr fills the page once
// every check run on starting has returned, after its host has started,
// so the test waits until the page holds entries, as a fresh install's
// always does (no indexer, no download client), and an empty page never
// passes for a clean one.
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

	health, err := healthChecked(t.Context(), s)
	checks.True("Lidarr's Health page fills once its checks have run", err == nil, errText(err))
	if err != nil {
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

// healthChecked asks for Lidarr's Health page every 250 ms until it lists
// an entry, and returns it then, or an error with the last answer once
// harness.ServeWithin has passed. Every answer short of that, a failed
// request or an empty page among them, is one to ask again after.
func healthChecked(ctx context.Context, s *harness.Serving) ([]healthCheck, error) {
	var health []healthCheck
	err := harness.Poll(ctx, harness.ServeWithin, 250*time.Millisecond, func(ctx context.Context) error {
		resp, err := api(ctx, s, http.MethodGet, "/api/v1/health", nil)
		switch {
		case err != nil:
			return harness.NotYet(err)
		case resp.Status != http.StatusOK:
			return harness.NotYet(fmt.Errorf("status %d", resp.Status))
		}
		var got []healthCheck
		if err := jsonInto(resp, &got); err != nil {
			return harness.NotYet(err)
		}
		if len(got) == 0 {
			return harness.NotYet(errors.New("the Health page lists nothing yet"))
		}
		health = got
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("GET /api/v1/health: %w", err)
	}
	return health, nil
}
