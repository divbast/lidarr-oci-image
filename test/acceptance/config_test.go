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

// Lidarr's first write to /config is its error reporter's folder,
// /config/Sentry, before its own check that the folder is writable, so a
// /config it cannot write fails there, with an exception and not
// upstream's startup message, and each failure below exits as measured.
const (
	// notWritable is what Lidarr logs when it may not write in /config.
	notWritable = "Access to the path '/config/Sentry' is denied"
	// notWritableExit is the exit status it then gives, an unhandled
	// error's, within two seconds.
	notWritableExit = 1
	// readOnly is what it logs with nothing mounted at /config, where the
	// image's own empty /config is on the read-only root. The error goes
	// unhandled, and the runtime ends the process within a second by a
	// signal of its own, whose number is the architecture's: 133, SIGTRAP,
	// on arm64, and 139 where amd64 was measured, under emulation.
	readOnly = "Read-only file system : '/config/Sentry'"
	// corrupt is what Lidarr says of a config.xml it cannot read.
	corrupt = "Lidarr failed to start: /config/config.xml is corrupt or invalid."
	// nonRecoverableFailure is the exit status Lidarr's ConsoleApp gives a
	// LidarrStartupException, which -exitimmediately has it exit with at
	// once rather than wait an hour for a key no one presses in a
	// container.
	nonRecoverableFailure = 3
)

// TestRefusesUnwritableConfig runs the image on a /config its user cannot
// write: a volume root owns, mode 0755, as a directory root made and bound
// in, or a volume an image run as root left, would be. The container must
// exit within a minute rather than serve with nowhere to keep its state,
// or wait to be told to stop, and its log must name /config, since the
// log is all a user sees of why.
func TestRefusesUnwritableConfig(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	vol := configVolume(t, e)
	prepare(t, e, image, asRoot, vol, "root-own")
	harness.Refuses(t, e, refusing(e, image, &vol), notWritableExit, "on a /config root owns", notWritable)
}

// TestLogWaitEndsWhenStopped holds the suite's own helper, not anything
// the image guarantees: logged, which the tests that wait for a line in
// Lidarr's log share, must return the moment the container stops, as
// it does on a /config root owns, rather than wait out its budget for a
// line that can no longer come. It asserts on the error, not on the time
// taken, which a slow runner would make flaky: had logged waited, its
// error would be Poll's, "still failing after", wrapping the stopped
// container's.
func TestLogWaitEndsWhenStopped(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	ctx := t.Context()
	vol := configVolume(t, e)
	prepare(t, e, image, asRoot, vol, "root-own")
	c, err := e.Start(ctx, t, refusing(e, image, &vol))
	if err != nil {
		t.Fatal(err)
	}
	err = logged(ctx, c, "a line Lidarr never logs", harness.ServeWithin)
	text := errText(err)
	harness.NewChecks(t).True("logged returns the stopped container's error at once, not its budget's",
		err != nil && strings.Contains(text, "the container stopped") && !strings.Contains(text, "still failing after"),
		harness.Clip(text))
}

// TestRefusesNoConfigMount runs the image with nothing mounted at /config,
// as a user who forgets the volume does: the image's own /config is empty
// and 65532's, on a read-only root. The container must exit within a
// minute, unsuccessfully, naming /config, rather than serve with nowhere
// to keep its state or sit there looking alive. The status is the
// runtime's signal, which differs between architectures, so any but 0
// will do.
func TestRefusesNoConfigMount(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	ctx := t.Context()
	checks := harness.NewChecks(t)
	c, err := e.Start(ctx, t, refusing(e, image, nil))
	if err != nil {
		t.Fatal(err)
	}
	code, err := c.WaitExit(ctx, harness.ServeWithin)
	if err != nil {
		checks.True("with no /config mounted, the container exits within "+harness.ServeWithin.String(), false, harness.WithLog(ctx, c, err).Error())
		return
	}
	checks.True("with no /config mounted, the container exits unsuccessfully", code != 0, fmt.Sprintf("exit status %d", code))
	harness.LogContains(t, c, "with no /config mounted, the log says why", readOnly)
}

// corruptConfig is a config.xml cut off part way, as a full disk or a
// power cut can leave one.
const corruptConfig = "<Config>\n  <Port>8686"

// TestExitsOnCorruptConfig runs the image on a /config whose config.xml
// is cut off. Lidarr cannot read it, and fails to start with a
// LidarrStartupException, which upstream calls non-recoverable: unless
// given -exitimmediately, it then waits an hour for a key that no one
// presses in a container. The container must exit within a minute with
// NonRecoverableFailure, the status only -exitimmediately gives that wait,
// and its log must say what to do.
func TestExitsOnCorruptConfig(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	vol := configVolume(t, e)
	prepare(t, e, image, harness.Contract(tmpfsSize), vol, "write", "config.xml", corruptConfig)
	harness.Refuses(t, e, refusing(e, image, &vol), nonRecoverableFailure, "with config.xml cut off", corrupt)
}

// refusing is a run of the image under the contract, with vol at /config,
// or nothing there when vol is nil, and no network, for a start Lidarr
// must refuse, under engine e, which keeps every line it logs.
func refusing(e harness.Engine, image string, vol *harness.Mount) *harness.RunSpec {
	spec := &harness.RunSpec{
		Image:   image,
		Network: "none",
		Confine: harness.Contract(tmpfsSize),
		Extra:   engineLogs(e),
	}
	if vol != nil {
		spec.Mounts = []harness.Mount{*vol}
	}
	return spec
}

// migratedConfig is a config.xml as a linuxserver or hotio Lidarr image
// leaves it in /config: upstream's port, 8686, no authentication, its own
// image's Docker updater on the master branch, and the API key that
// install made.
const migratedConfig = `<Config>
  <BindAddress>*</BindAddress>
  <Port>8686</Port>
  <SslPort>6868</SslPort>
  <EnableSsl>False</EnableSsl>
  <LaunchBrowser>True</LaunchBrowser>
  <ApiKey>` + testAPIKey + `</ApiKey>
  <AuthenticationMethod>None</AuthenticationMethod>
  <AuthenticationRequired>Enabled</AuthenticationRequired>
  <Branch>master</Branch>
  <LogLevel>info</LogLevel>
  <UrlBase></UrlBase>
  <InstanceName>Lidarr</InstanceName>
  <UpdateMechanism>Docker</UpdateMechanism>
</Config>
`

// branchChanged is what Lidarr logs as it changes config.xml's branch to
// the nightly branch package_info names, once its host has started.
const branchChanged = "External updater uses branch nightly instead of the currently selected master, changing to nightly."

// TestMigratedConfig runs the image on a /config another image left, whose
// config.xml says port 8686, no authentication, and the Docker updater on
// master, beside an empty plugins folder, with no API key in the
// environment. It must serve on 8080, which the image sets over
// config.xml; answer the API with config.xml's key; keep its owner's
// choice of no login; once its host has started, have changed config.xml's
// branch to nightly, Docker staying the updater; list no plugins; and be
// healthy, the probe reading the API key from config.xml.
func TestMigratedConfig(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	vol := configVolume(t, e)
	prepare(t, e, image, harness.Contract(tmpfsSize), vol, "write", "config.xml", migratedConfig)
	prepare(t, e, image, harness.Contract(tmpfsSize), vol, "mkdir", "plugins")
	s := serveLidarr(t, e, image, pingPath, nil, vol)
	checks := harness.NewChecks(t)

	harness.StatusIs(t, s, "GET /ping on 8080, from the host, answers 200", http.StatusOK, pingPath)
	err := logged(t.Context(), s.C, hostStarted, harness.ServeWithin)
	checks.True("Lidarr's host finishes starting", err == nil, errText(err))
	harness.LogContains(t, s.C, "Lidarr says it changes master to nightly", branchChanged)
	host, ok := callAPI[hostSettings](t, s, "GET /api/v1/config/host with config.xml's key answers 200", http.StatusOK,
		http.MethodGet, "/api/v1/config/host", nil)
	if ok {
		harness.Equal(checks, "config.xml's update mechanism is docker", dockerMechanism, host.UpdateMechanism)
		harness.Equal(checks, "config.xml's branch is nightly", nightlyBranch, host.Branch)
		harness.Equal(checks, "the port in force is 8080", appPort, host.Port)
	}
	status, ok := callAPI[systemStatus](t, s, "GET /api/v1/system/status with config.xml's key answers 200", http.StatusOK,
		http.MethodGet, "/api/v1/system/status", nil)
	if ok {
		harness.Equal(checks, "the authentication in force is none, as config.xml says", "none", status.Authentication)
		harness.Equal(checks, "Lidarr's branch is nightly", nightlyBranch, status.Branch)
		harness.Equal(checks, "Lidarr's update mechanism is docker", dockerMechanism, status.PackageUpdateMechanism)
	}
	plugins, ok := callAPI[[]plugin](t, s, "GET /api/v1/system/plugins answers 200", http.StatusOK,
		http.MethodGet, pluginsPath, nil)
	if ok {
		harness.Equal(checks, "with an empty plugins folder, no plugin is listed", 0, len(plugins))
	}
	healthy(t, s, "the probe, with config.xml's key, exits 0 and prints nothing")
}
