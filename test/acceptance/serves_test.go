// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"mime"
	"net/http"
	"testing"

	"github.com/divbast/ci/harness"
)

// pingBody is exactly what /ping answers once Lidarr is up: upstream's
// JSON, indented, as it sends it.
const pingBody = "{\n  \"status\": \"OK\"\n}"

// loginTitle is the title of the UI's login page, which only the UI the
// frontend stage built carries, and which Lidarr serves to anyone.
const loginTitle = "<title>Login - Lidarr</title>"

// TestServes checks the app port from a client outside the container,
// through the published port, with no session and no API key, as a fresh
// install meets a visitor: /ping answers 200 with exactly upstream's body;
// the login page answers 200 with the UI's HTML; the API refuses a client
// without the key with 401; and, asked with the key, a fresh install says
// it asks for no login, as upstream ships it, until its owner chooses a
// method, which the README says to do first.
func TestServes(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	ctx := t.Context()
	s := serveFresh(t, e, image)
	checks := harness.NewChecks(t)

	resp, err := s.Get(ctx, pingPath, nil)
	harness.Answers(t, "GET /ping from the host answers 200", http.StatusOK, resp, err)
	if err == nil {
		harness.Equal(checks, "GET /ping from the host is exactly upstream's body", pingBody, string(resp.Body))
	}

	resp, err = s.Get(ctx, "/login", nil)
	harness.Answers(t, "GET /login from the host answers 200", http.StatusOK, resp, err)
	if err == nil {
		media, _, merr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if merr != nil {
			media = merr.Error()
		}
		harness.Equal(checks, "GET /login from the host is HTML", "text/html", media)
		harness.Contains(t, "GET /login from the host is the UI's login page", loginTitle, string(resp.Body))
	}

	harness.StatusIs(t, s, "GET /api/v1/system/status from the host without the API key answers 401",
		http.StatusUnauthorized, "/api/v1/system/status")

	status, ok := callAPI[systemStatus](t, s, "GET /api/v1/system/status with the key answers 200", http.StatusOK,
		http.MethodGet, "/api/v1/system/status", nil)
	if ok {
		harness.Equal(checks, "a fresh install's authentication is none", "none", status.Authentication)
		harness.Equal(checks, "the app is Lidarr", "Lidarr", status.AppName)
	}
}
