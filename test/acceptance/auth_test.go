// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"net/http"
	"strings"
	"testing"

	"github.com/divbast/ci/harness"
)

// TestHealthyUnderEveryAuthMethod runs the image under each authentication
// method, and under Forms with local addresses let through, and checks
// the probe stays healthy: it sends the API key, which every method
// accepts, so neither half needs a session. Each method must be the one in
// force, as the status gives it, or the probe would only have been run
// under None four times. Oidc is left out: in force, it fetches its
// provider's metadata over the network, which no container here reaches.
func TestHealthyUnderEveryAuthMethod(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	for _, tc := range []struct{ method, required, inForce string }{
		{method: "None", required: "Enabled", inForce: "none"},
		{method: "Forms", required: "Enabled", inForce: "forms"},
		{method: "External", required: "Enabled", inForce: "external"},
		{method: "Forms", required: "DisabledForLocalAddresses", inForce: "forms"},
	} {
		t.Run(tc.method+"/"+tc.required, func(t *testing.T) {
			t.Parallel()
			s := serveLidarr(t, e, image, pingPath, keyed(map[string]string{
				"LIDARR__AUTH__METHOD":   tc.method,
				"LIDARR__AUTH__REQUIRED": tc.required,
			}), configVolume(t, e))

			status, ok := callAPI[systemStatus](t, s, "GET /api/v1/system/status answers 200", http.StatusOK,
				http.MethodGet, "/api/v1/system/status", nil)
			if ok {
				harness.Equal(harness.NewChecks(t), "the authentication in force is "+tc.inForce, tc.inForce, status.Authentication)
			}
			healthy(t, s, "under "+tc.method+" with authentication "+tc.required+", the probe exits 0 and prints nothing")
		})
	}
}

// TestFormsSignIn makes the first user as a new install's owner does, and
// signs in as them through the login form: the right password sends the
// browser on with Lidarr's session cookie, a wrong one back to the login
// page with none. Once a login is asked for, a visitor with no session is
// sent to the login page.
func TestFormsSignIn(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	ctx := t.Context()
	s := serveSignedIn(t, e, image)
	checks := harness.NewChecks(t)

	status, ok := callAPI[systemStatus](t, s, "GET /api/v1/system/status answers 200", http.StatusOK,
		http.MethodGet, "/api/v1/system/status", nil)
	if ok {
		harness.Equal(checks, "once the first user is made, the authentication in force is forms", "forms", status.Authentication)
	}
	signIn(t, s)

	resp, err := postLogin(ctx, s, "not-the-password")
	harness.Answers(t, "POST /login with a wrong password answers 302", http.StatusFound, resp, err)
	if err == nil {
		where := resp.Header.Get("Location")
		checks.True("a wrong password goes back to the login page, saying it failed", strings.Contains(where, "loginFailed=true"), "Location: "+where)
		for _, c := range resp.Header.Values("Set-Cookie") {
			name, _, _ := strings.Cut(c, "=")
			checks.True("a wrong password sets no session cookie", name != sessionCookie, c)
		}
	}

	resp, err = s.Get(ctx, "/", nil)
	harness.Answers(t, "GET / with no session answers 302", http.StatusFound, resp, err)
	if err == nil {
		where := resp.Header.Get("Location")
		checks.True("GET / with no session goes to the login page", strings.Contains(where, "/login"), "Location: "+where)
	}
}
