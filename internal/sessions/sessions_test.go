//go:build !windows

package sessions

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestSummary(t *testing.T) {
	one := []Orphan{{Children: []Child{{Project: "api", Pid: 4123}}}}
	if got := Summary(one); got != "1 process left over from a previous runr session: api (pid 4123)" {
		t.Errorf("one: %q", got)
	}
	two := []Orphan{{Children: []Child{{Project: "api", Pid: 4123}}}, {Children: []Child{{Project: "db", Pid: 4120}}}}
	if got := Summary(two); got != "2 processes left over from previous runr sessions: api (pid 4123), db (pid 4120)" {
		t.Errorf("two: %q", got)
	}
}

// spawn starts a command in its own process group, like runr does, and
// reaps it when it exits (so it doesn't linger as a zombie).
func spawn(t *testing.T, script string) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	t.Cleanup(func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	return cmd.Process.Pid
}

// gonePid returns the pid of a process that has exited.
func gonePid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func child(t *testing.T, project string, pid int) Child {
	t.Helper()
	start, err := procStart(pid)
	if err != nil {
		t.Fatal(err)
	}
	return Child{Project: project, Pid: pid, Pgid: pid, Start: start, Command: "cmd of " + project}
}

func writeState(t *testing.T, dir string, s State) string {
	t.Helper()
	path := filepath.Join(dir, strconv.Itoa(s.Pid)+"-x.json")
	b, _ := json.Marshal(s)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestRecorder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	read := func() State {
		t.Helper()
		b, err := os.ReadFile(r.path)
		if err != nil {
			t.Fatal(err)
		}
		var s State
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	own, _ := procStart(os.Getpid())
	if s := read(); s.Pid != os.Getpid() || s.Start != own || len(s.Children) != 0 {
		t.Errorf("state = %+v", s)
	}

	api := spawn(t, "sleep 30")
	r.Spawned("api", api, "go run .", "/code/api", "")
	r.Spawned("gone", gonePid(t), "true", "", "") // exited before it was recorded: skipped
	s := read()
	if len(s.Children) != 1 || s.Children[0].Project != "api" || s.Children[0].Pid != api ||
		s.Children[0].Pgid != api || s.Children[0].Command != "go run ." || s.Children[0].Start == "" || s.Children[0].Dir != "/code/api" {
		t.Errorf("state = %+v", s)
	}
	// A live session is not an orphan, and its file stays.
	if orphans, err := Find(dir); err != nil || len(orphans) != 0 || !exists(r.path) {
		t.Errorf("Find = %v, %v", orphans, err)
	}
	r.Exited("api")
	if s := read(); len(s.Children) != 0 {
		t.Errorf("after exit: %+v", s)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("temporary files left: %v", entries)
	}
	r.Close()
	if exists(r.path) {
		t.Error("file kept after Close")
	}
	var none *Recorder // nil records nothing
	none.Spawned("x", 1, "y", "", "")
	none.Exited("x")
	none.Close()
}

func TestFindAndKill(t *testing.T) {
	dir := t.TempDir()
	api, db := spawn(t, "sleep 30"), spawn(t, "sleep 30")
	reused := spawn(t, "sleep 30") // a pid that is now another process
	stale := child(t, "web", reused)
	stale.Start = "1"
	orphanFile := writeState(t, dir, State{Pid: gonePid(t), Start: "1", Children: []Child{
		child(t, "api", api), child(t, "db", db), stale,
	}})
	emptyFile := writeState(t, dir, State{Pid: gonePid(t), Start: "1", Children: []Child{{Project: "x", Pid: gonePid(t), Pgid: 1 << 22, Start: "1"}}})
	broken := filepath.Join(dir, "broken.json")
	os.WriteFile(broken, []byte("{"), 0o600)

	orphans, err := Find(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || orphans[0].Path != orphanFile || len(orphans[0].Children) != 2 ||
		orphans[0].Children[0].Pid != api || orphans[0].Children[1].Pid != db {
		t.Fatalf("orphans = %+v", orphans)
	}
	if exists(emptyFile) || exists(broken) {
		t.Error("files without live children are kept")
	}

	start := time.Now()
	if forced := Kill(orphans, 5*time.Second); len(forced) != 0 {
		t.Errorf("forced = %+v", forced)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Kill took %v", d)
	}
	for _, pid := range []int{api, db} {
		if groupExists(pid) {
			t.Errorf("group %d still runs", pid)
		}
	}
	if !groupExists(reused) {
		t.Error("a process with a reused pid was killed")
	}
	if exists(orphanFile) {
		t.Error("orphan file kept after Kill")
	}
}

func TestKillForces(t *testing.T) {
	dir := t.TempDir()
	stubborn := spawn(t, "trap '' TERM; sleep 30")
	time.Sleep(50 * time.Millisecond) // let the shell set its trap
	writeState(t, dir, State{Pid: gonePid(t), Start: "1", Children: []Child{child(t, "stubborn", stubborn)}})
	orphans, _ := Find(dir)
	forced := Kill(orphans, 100*time.Millisecond)
	if len(forced) != 1 || forced[0].Pid != stubborn {
		t.Errorf("forced = %+v", forced)
	}
	if groupExists(stubborn) {
		t.Error("still running after SIGKILL")
	}
}

func TestForget(t *testing.T) {
	dir := t.TempDir()
	api := spawn(t, "sleep 30")
	path := writeState(t, dir, State{Pid: gonePid(t), Start: "1", Children: []Child{child(t, "api", api)}})
	orphans, _ := Find(dir)
	Forget(orphans)
	if exists(path) || !groupExists(api) {
		t.Error("Forget should delete the file and leave the process")
	}
}

func TestFindKeepsStopCmds(t *testing.T) {
	dir := t.TempDir()
	db := spawn(t, "sleep 30")
	live := child(t, "db", db)
	live.StopCmd = "docker compose down"
	path := writeState(t, dir, State{Pid: gonePid(t), Start: "1", Children: []Child{
		live,
		{Project: "cache", Pid: gonePid(t), Pgid: 1 << 22, Start: "1", StopCmd: "docker compose stop cache", Dir: "/code/infra"},
		{Project: "api", Pid: gonePid(t), Pgid: 1<<22 + 1, Start: "1"}, // gone, nothing to run: dropped
	}})
	orphans, err := Find(dir)
	if err != nil || len(orphans) != 1 {
		t.Fatalf("Find = %+v, %v", orphans, err)
	}
	cs := orphans[0].Children
	if len(cs) != 2 || cs[0].Project != "db" || cs[0].Gone || cs[1].Project != "cache" || !cs[1].Gone || cs[1].Dir != "/code/infra" {
		t.Fatalf("children = %+v", cs)
	}
	if got := Summary(orphans); got != "2 processes left over from a previous runr session: db (pid "+strconv.Itoa(db)+"), cache (gone; stop_cmd to run)" {
		t.Errorf("Summary = %q", got)
	}
	if forced := Kill(orphans, time.Second); len(forced) != 0 {
		t.Errorf("forced = %+v", forced)
	}
	if groupExists(db) || exists(path) {
		t.Error("db still runs, or the file is kept")
	}
}
