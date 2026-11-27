// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package acceptance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/divbast/ci/harness"
)

// seedTags is how many tags the test makes before the backup, so the
// database it backs up holds what the API wrote: as many as tmpfsSize was
// measured with.
const seedTags = 50

// backupWithin bounds the backup command: it took under a second when
// tmpfsSize was measured.
const backupWithin = time.Minute

// command is a command as Lidarr's API gives it.
type command struct {
	ID      int    `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// backup is a backup as GET /api/v1/system/backup lists it.
type backup struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
	Size int    `json:"size"`
}

// TestBackupFitsTmpfs checks that a backup fits the tmpfs the contract
// grants: Lidarr copies the database and config.xml into /tmp before it
// zips them into /config. A Backup command sent through the API, after 50
// tags, completes; GET /api/v1/system/backup lists the backup it made;
// and downloading it as the UI does, signed in, answers 200 with every
// byte the listing gives.
func TestBackupFitsTmpfs(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	ctx := t.Context()
	s := serveSignedIn(t, e, image)
	checks := harness.NewChecks(t)

	made, last := 0, ""
	for i := range seedTags {
		resp, err := api(ctx, s, http.MethodPost, "/api/v1/tag", tag{Label: "seed-" + strconv.Itoa(i)})
		switch {
		case err != nil:
			last = err.Error()
		case resp.Status != http.StatusCreated:
			last = fmt.Sprintf("status %d: %s", resp.Status, harness.Clip(string(resp.Body)))
		default:
			made++
		}
	}
	detail := fmt.Sprintf("%d made", made)
	if made != seedTags {
		detail += "; the last refused with " + last
	}
	checks.True(fmt.Sprintf("POST /api/v1/tag makes %d tags", seedTags), made == seedTags, detail)

	cmd, ok := callAPI[command](t, s, "POST /api/v1/command for a Backup answers 201", http.StatusCreated,
		http.MethodPost, "/api/v1/command", map[string]string{"name": "Backup"})
	if !ok {
		return
	}
	done, err := finished(ctx, s, cmd.ID)
	if err != nil {
		checks.True("the Backup command finishes within "+backupWithin.String(), false, err.Error())
		return
	}
	checks.True("the Backup command completes", done.Status == "completed",
		fmt.Sprintf("status %q: %s", done.Status, done.Message))

	backups, ok := callAPI[[]backup](t, s, "GET /api/v1/system/backup answers 200", http.StatusOK,
		http.MethodGet, "/api/v1/system/backup", nil)
	if !ok {
		return
	}
	checks.True("GET /api/v1/system/backup lists one backup, a manual one", len(backups) == 1 && backups[0].Type == "manual",
		fmt.Sprintf("%+v", backups))
	if len(backups) != 1 {
		return
	}
	cookie, ok := signIn(t, s)
	if !ok {
		return
	}
	resp, err := s.Get(ctx, backups[0].Path, http.Header{"Cookie": {cookie}})
	harness.Answers(t, "GET "+backups[0].Path+", signed in, answers 200", http.StatusOK, resp, err)
	if err == nil {
		harness.Equal(checks, "the download is as long as the listing says", backups[0].Size, len(resp.Body))
	}
}

// finished asks for command id every 250 ms until it is no longer queued
// or started, and returns it then, or an error with the last answer once
// backupWithin has passed. Every answer short of that, a failed request
// among them, is one to ask again after.
func finished(ctx context.Context, s *harness.Serving, id int) (command, error) {
	path := "/api/v1/command/" + strconv.Itoa(id)
	var done command
	err := harness.Poll(ctx, backupWithin, 250*time.Millisecond, func(ctx context.Context) error {
		resp, err := api(ctx, s, http.MethodGet, path, nil)
		switch {
		case err != nil:
			return harness.NotYet(err)
		case resp.Status != http.StatusOK:
			return harness.NotYet(fmt.Errorf("status %d", resp.Status))
		}
		var cmd command
		if err := jsonInto(resp, &cmd); err != nil {
			return harness.NotYet(err)
		}
		if cmd.Status == "queued" || cmd.Status == "started" {
			return harness.NotYet(errors.New("command status " + cmd.Status))
		}
		done = cmd
		return nil
	})
	if err != nil {
		return command{}, fmt.Errorf("GET %s: %w", path, err)
	}
	return done, nil
}

// TestRestoresBackup restores a backup as a user moving between images
// does, through the UI's upload: a tag is made and backed up, the backup
// downloaded, the tag deleted, and the backup uploaded to
// /api/v1/system/backup/restore/upload, which Lidarr stages in /tmp and
// applies once it restarts. Restarted as the UI then restarts it, in the
// one process, Lidarr must say it is restoring the database. That restart
// does not clear SQLite's connection pool, which upstream clears only on
// a full shutdown, so the new host can go on reading the database it
// replaced, through a connection the old one left open: on a slow runner
// the tag stayed gone. A restore that fails partway does not stop Lidarr
// at once, as it stops Sonarr with exit status 3: if its plugins unload,
// Lidarr builds its host again without them, and that host runs the
// staged restore again. If they do not unload, or the second try fails
// too, Lidarr stops, with exit status 0, the restore still staged. Which
// way it went is logged, never checked. The restore stays staged until it
// is applied, so a restart of the container, which the README tells users
// to make after a restore, applies it whatever the pool held, from a
// container still running or one that has stopped. Lidarr must then serve
// again, with the tag back.
func TestRestoresBackup(t *testing.T) {
	t.Parallel()
	e, image := underTest(t)
	ctx := t.Context()
	s := serveSignedIn(t, e, image)
	checks := harness.NewChecks(t)

	made, ok := callAPI[tag](t, s, "POST /api/v1/tag answers 201", http.StatusCreated,
		http.MethodPost, "/api/v1/tag", tag{Label: "restored"})
	if !ok {
		return
	}
	zip, ok := backupNow(t, s)
	if !ok {
		return
	}
	resp, err := api(ctx, s, http.MethodDelete, "/api/v1/tag/"+strconv.Itoa(made.ID), nil)
	checks.True("DELETE /api/v1/tag/<its id> succeeds", err == nil && resp.Status < 300, fmt.Sprintf("%v %d", err, resp.Status))
	// Without the tag gone, the restore below would have nothing to bring
	// back, and the check after it would pass whatever the restore did
	gone := labels(t, s, "after the delete")
	if gone == nil {
		return
	}
	checks.True("after the delete, the tag is gone", !slices.Contains(gone, "restored"), fmt.Sprintf("tags: %q", gone))

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "lidarr_backup.zip")
	if err == nil {
		_, err = part.Write(zip)
	}
	if err == nil {
		err = form.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	resp, err = s.Do(ctx, nil, http.MethodPost, "/api/v1/system/backup/restore/upload",
		http.Header{"X-Api-Key": {testAPIKey}, "Content-Type": {form.FormDataContentType()}}, body.Bytes())
	harness.Answers(t, "POST /api/v1/system/backup/restore/upload with the backup answers 200", http.StatusOK, resp, err)
	if err != nil || resp.Status != http.StatusOK {
		return
	}

	resp, err = api(ctx, s, http.MethodPost, "/api/v1/system/restart", nil)
	harness.Answers(t, "POST /api/v1/system/restart answers 200", http.StatusOK, resp, err)
	err = logged(ctx, s.C, restoringDatabase, restartWithin)
	checks.True("Lidarr says it is restoring the database within "+restartWithin.String(), err == nil, errText(err))

	// Upstream's race: its in-process restart keeps the SQLite connection
	// pool the old host left, so by now Lidarr may be serving the database
	// the restore replaced, or, the restore having failed, be trying it
	// again without plugins, or have stopped with exit status 0. The
	// container restarts whatever state it is in, as the README tells users
	// to: an on-failure restart policy would not restart one that exited 0
	noteOutcome(t, s.C)
	if err = s.Restart(ctx); err != nil {
		t.Fatalf("restarting the container after the restore began: %v", err)
	}
	err = servesAgain(ctx, s)
	checks.True("after a restart of the container, /ping answers 200 within "+restartWithin.String(), err == nil, errText(err))
	if got := labels(t, s, "after the restore and a restart of the container"); got != nil {
		checks.True("after the restore and a restart of the container, the tag is back", slices.Contains(got, "restored"), fmt.Sprintf("tags: %q", got))
	}
}

// restoringDatabase is what Lidarr logs as the host a restart builds
// begins to put the restored database in place of the old one.
const restoringDatabase = "DatabaseRestorationService: Restoring Database"

// restoreFailed is what Lidarr logs when the restore fails partway, which
// fails the host's start.
const restoreFailed = "DatabaseRestorationService: Failed to restore database"

// failedWithPlugins is what Lidarr logs when a host fails to start with
// plugins. If its plugins then unload, Lidarr builds the host again
// without them, and after a restore that failed, that host runs the
// restore again.
const failedWithPlugins = "Bootstrap: Error starting with plugins enabled"

// noteOutcome logs, for the record and never as a check, how the
// in-process restart has left c just before the test restarts it: its
// state, with its exit status once it has stopped, whether its log says
// the restore failed, and whether a host failed to start with plugins.
// Plugins that do not unload, or a second failure in a row, end Lidarr
// with exit status 0. CI's history then shows how often each way of
// upstream's race happens, and when upstream's fix ends it. It runs as
// soon as the restore has begun, so a failure, a retry or an exit that
// comes after that look is not in its line.
func noteOutcome(t *testing.T, c *harness.Container) {
	t.Helper()
	ctx := t.Context()
	var state string
	switch ins, err := c.Inspect(ctx); {
	case err != nil:
		state = "of a state the engine did not give: " + err.Error()
	case ins.State.Running:
		state = ins.State.Status
	default:
		state = fmt.Sprintf("%s, exit status %d", ins.State.Status, ins.State.ExitCode)
	}
	said := "its log could not be read"
	if logs, err := c.Logs(ctx); err == nil {
		said = fmt.Sprintf("%q logged: %t; %q logged: %t",
			restoreFailed, strings.Contains(logs, restoreFailed), failedWithPlugins, strings.Contains(logs, failedWithPlugins))
	}
	t.Logf("before the restart of the container, the in-process restart left it %s; %s", state, said)
}

// backupNow has s back itself up, as a Backup command through the API,
// and returns the backup's bytes as the UI downloads them, signed in, or
// false, having failed a check, when it cannot. s must have been served
// with [serveSignedIn].
func backupNow(t *testing.T, s *harness.Serving) ([]byte, bool) {
	t.Helper()
	ctx := t.Context()
	checks := harness.NewChecks(t)
	cmd, ok := callAPI[command](t, s, "POST /api/v1/command for a Backup answers 201", http.StatusCreated,
		http.MethodPost, "/api/v1/command", map[string]string{"name": "Backup"})
	if !ok {
		return nil, false
	}
	done, err := finished(ctx, s, cmd.ID)
	if err != nil || done.Status != "completed" {
		checks.True("the Backup command completes within "+backupWithin.String(), false, fmt.Sprintf("%v %+v", err, done))
		return nil, false
	}
	backups, ok := callAPI[[]backup](t, s, "GET /api/v1/system/backup answers 200", http.StatusOK,
		http.MethodGet, "/api/v1/system/backup", nil)
	if !ok || len(backups) == 0 {
		checks.True("GET /api/v1/system/backup lists the backup", false, fmt.Sprintf("%+v", backups))
		return nil, false
	}
	cookie, ok := signIn(t, s)
	if !ok {
		return nil, false
	}
	resp, err := s.Get(ctx, backups[0].Path, http.Header{"Cookie": {cookie}})
	harness.Answers(t, "GET "+backups[0].Path+", signed in, answers 200", http.StatusOK, resp, err)
	if err != nil || resp.Status != http.StatusOK {
		return nil, false
	}
	return resp.Body, true
}

// labels returns the labels of every tag s's API lists, as the check
// when, or nil, having failed a check, when it cannot.
func labels(t *testing.T, s *harness.Serving, when string) []string {
	t.Helper()
	tags, ok := callAPI[[]tag](t, s, when+", GET /api/v1/tag answers 200", http.StatusOK,
		http.MethodGet, "/api/v1/tag", nil)
	if !ok {
		return nil
	}
	got := []string{}
	for _, tg := range tags {
		got = append(got, tg.Label)
	}
	return got
}
