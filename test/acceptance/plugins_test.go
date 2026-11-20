// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"net/http"
	"testing"

	"github.com/divbast/ci/harness"
)

// pluginsDir is where Lidarr keeps the plugins its owner installs, under
// its data directory: /app is read-only, so plugins live nowhere else.
// A fresh install has none, and no folder, until the first is installed.
const pluginsDir = configDir + "/plugins"

// pluginsPath is the API route that lists the installed plugins, which
// the UI's System, Plugins page reads.
const pluginsPath = "/api/v1/system/plugins"

// plugin is a plugin as GET /api/v1/system/plugins lists it.
type plugin struct {
	Name    string `json:"name"`
	Owner   string `json:"owner"`
	Version string `json:"installedVersion"`
}

// TestPluginsEmpty checks the plugin system answers on a fresh install,
// with nothing installed: the page a user goes to to install one lists
// none, rather than failing. No plugin is installed here, which would
// need GitHub and code no one has reviewed; the README's proof of one
// install was made by hand.
func TestPluginsEmpty(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	s := serveFresh(t, e, image)

	plugins, ok := callAPI[[]plugin](t, s, "GET "+pluginsPath+" answers 200", http.StatusOK,
		http.MethodGet, pluginsPath, nil)
	if ok {
		harness.Equal(harness.NewChecks(t), "a fresh install lists no plugin", 0, len(plugins))
	}
}
