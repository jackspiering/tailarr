package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackspiering/tailarr/internal/config"
)

// testConfig returns a Config whose paths live under root. No directories
// are created; tests decide what exists.
func testConfig(root string) config.Config {
	return config.Config{
		ConfigPath:   filepath.Join(root, "etc", "tailarr.conf"),
		RepoURL:      "https://github.com/example/scaletail",
		RepoPath:     filepath.Join(root, "opt", "scaletail"),
		DeployPath:   filepath.Join(root, "srv", "tailarr"),
		LogPath:      filepath.Join(root, "var", "log", "tailarr.log"),
		AuthkeysPath: filepath.Join(root, "var", "lib", "authkeys.json"),
	}
}

// readyRoot creates every parent directory Run checks, plus the ScaleTail
// services directory, so path checks report ok.
func readyRoot(t *testing.T) (string, config.Config) {
	t.Helper()
	root := t.TempDir()
	cfg := testConfig(root)
	for _, dir := range []string{
		// Run checks the deploy path itself; every other check targets the
		// parent of its configured path.
		filepath.Dir(cfg.ConfigPath),
		filepath.Dir(cfg.RepoPath),
		cfg.DeployPath,
		filepath.Dir(cfg.LogPath),
		filepath.Dir(cfg.AuthkeysPath),
		filepath.Join(cfg.RepoPath, "services"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root, cfg
}

// fakeDocker writes an executable `docker` script into dir and puts dir on
// PATH. The script receives the docker arguments as "$@".
func fakeDocker(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "docker")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// find returns the check with the given name.
func find(r Result, name string) *Check {
	for i := range r.Checks {
		if r.Checks[i].Name == name {
			return &r.Checks[i]
		}
	}
	return nil
}

func TestHealthyFalseOnlyOnFail(t *testing.T) {
	t.Parallel()
	ok := Result{Checks: []Check{
		{Level: OK, Name: "a"},
		{Level: Warn, Name: "b"},
	}}
	if !ok.Healthy() {
		t.Fatal("result with ok and warn checks must be healthy")
	}
	bad := Result{Checks: append(ok.Checks, Check{Level: Fail, Name: "c"})}
	if bad.Healthy() {
		t.Fatal("result with a fail check must not be healthy")
	}
	if !(Result{}).Healthy() {
		t.Fatal("empty result must be healthy")
	}
}

func TestRunFailsClosedWithoutGitAndDocker(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	r := Run(testConfig(t.TempDir()))
	git, docker := find(r, "git"), find(r, "docker")
	if git == nil || git.Level != Fail || !strings.Contains(git.Message, "not found in PATH") {
		t.Fatalf("git check must fail without PATH entry, got %+v", git)
	}
	if docker == nil || docker.Level != Fail {
		t.Fatalf("docker check must fail without PATH entry, got %+v", docker)
	}
	if find(r, "compose") != nil {
		t.Fatal("compose probe must be skipped when docker is missing")
	}
	if r.Healthy() {
		t.Fatal("run without git and docker must not be healthy")
	}
}

func TestRunReportsComposeAndDaemonOKWithFakeDocker(t *testing.T) {
	fakeDocker(t, "exit 0")

	_, cfg := readyRoot(t)
	r := Run(cfg)
	compose, daemon := find(r, "compose"), find(r, "daemon")
	if compose == nil || compose.Level != OK {
		t.Fatalf("compose check must pass with succeeding fake docker, got %+v", compose)
	}
	if daemon == nil || daemon.Level != OK {
		t.Fatalf("daemon check must pass with succeeding fake docker, got %+v", daemon)
	}
	if !r.Healthy() {
		t.Fatalf("ready host with working probes must be healthy, got %+v", r.Checks)
	}
	for _, label := range []string{"config dir", "ScaleTail parent", "deploy path", "log dir", "authkeys dir"} {
		if c := find(r, label); c == nil || c.Level != OK {
			t.Fatalf("%s check must pass in ready root, got %+v", label, c)
		}
	}
	// Write probes clean up after themselves.
	entries, err := os.ReadDir(filepath.Dir(cfg.ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tailarr-doctor-write-probe") {
			t.Fatalf("write probe left behind: %s", e.Name())
		}
	}
}

func TestRunWarnsWhenDaemonUnreachable(t *testing.T) {
	fakeDocker(t, `if [ "$1" = "compose" ]; then exit 0; fi
exit 1`)

	_, cfg := readyRoot(t)
	r := Run(cfg)
	daemon := find(r, "daemon")
	if daemon == nil || daemon.Level != Warn || !strings.Contains(daemon.Message, "not accessible") {
		t.Fatalf("unreachable daemon must warn, got %+v", daemon)
	}
	if !r.Healthy() {
		t.Fatal("unreachable daemon is a warning, not a failure")
	}
}

func TestRunFailsWhenComposeProbeFails(t *testing.T) {
	fakeDocker(t, "exit 1")

	_, cfg := readyRoot(t)
	r := Run(cfg)
	compose := find(r, "compose")
	if compose == nil || compose.Level != Fail || !strings.Contains(compose.Message, "Compose v2") {
		t.Fatalf("failing compose probe must fail the run, got %+v", compose)
	}
	if r.Healthy() {
		t.Fatal("missing compose support must not be healthy")
	}
}

func TestRunRefusesSymlinkedConfigDir(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(root)
	cfg.ConfigPath = filepath.Join(link, "tailarr.conf")

	r := Run(cfg)
	c := find(r, "config dir")
	if c == nil || c.Level != Fail || !strings.Contains(c.Message, "must not be a symlink") {
		t.Fatalf("symlinked config dir must fail closed, got %+v", c)
	}
}

func TestRunRefusesSymlinkAncestryInDeployParent(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real", "sub")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "lnk")); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(root)
	cfg.DeployPath = filepath.Join(root, "lnk", "sub", "deployroot")

	r := Run(cfg)
	c := find(r, "deploy path")
	if c == nil || c.Level != Fail || !strings.Contains(c.Message, "must not be a symlink") {
		t.Fatalf("symlinked ancestry must fail closed, got %+v", c)
	}
	if r.Healthy() {
		t.Fatal("symlinked ancestry must not be healthy")
	}
}

func TestRunFlagsSymlinkedRepoAndDeployPathsAsFail(t *testing.T) {
	root := t.TempDir()
	repoTarget := filepath.Join(root, "repo-target")
	deployTarget := filepath.Join(root, "deploy-target")
	for _, dir := range []string{repoTarget, deployTarget} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := testConfig(root)
	// Symlink parents must exist for os.Symlink to place the links.
	for _, parent := range []string{filepath.Dir(cfg.RepoPath), filepath.Dir(cfg.DeployPath)} {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(repoTarget, cfg.RepoPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(deployTarget, cfg.DeployPath); err != nil {
		t.Fatal(err)
	}

	r := Run(cfg)
	repo, deploy := find(r, "repo"), find(r, "deploy")
	if repo == nil || repo.Level != Fail || !strings.Contains(repo.Message, "ScaleTail path") {
		t.Fatalf("symlinked repo path must fail, got %+v", repo)
	}
	if deploy == nil || deploy.Level != Fail || !strings.Contains(deploy.Message, "deployment root") {
		t.Fatalf("symlinked deploy path must fail, got %+v", deploy)
	}
}

func TestRunWarnsOnSymlinkedConfigFileOnly(t *testing.T) {
	root := t.TempDir()
	etc := filepath.Join(root, "etc")
	target := filepath.Join(root, "elsewhere.conf")
	if err := os.MkdirAll(etc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("KEY=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(root)
	if err := os.Symlink(target, cfg.ConfigPath); err != nil {
		t.Fatal(err)
	}

	r := Run(cfg)
	dir := find(r, "config dir")
	if dir == nil || dir.Level != OK {
		t.Fatalf("config parent dir itself is fine, got %+v", dir)
	}
	c := find(r, "config")
	if c == nil || c.Level != Warn || !strings.Contains(c.Message, "symlink") {
		t.Fatalf("symlinked config file must warn, got %+v", c)
	}
	if !r.Healthy() {
		t.Fatal("a symlinked config file is a warning, not a failure")
	}
}

func TestRunFailsWhenConfigParentIsFile(t *testing.T) {
	root := t.TempDir()
	notDir := filepath.Join(root, "notadir")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(root)
	cfg.ConfigPath = filepath.Join(notDir, "tailarr.conf")

	r := Run(cfg)
	c := find(r, "config dir")
	if c == nil || c.Level != Fail || !strings.Contains(c.Message, "not a directory") {
		t.Fatalf("file where a directory belongs must fail, got %+v", c)
	}
}

func TestRunWarnsWhenLogDirNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("write probe always succeeds for root")
	}
	_, cfg := readyRoot(t)
	logDir := filepath.Dir(cfg.LogPath)
	if err := os.Chmod(logDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(logDir, 0o755) })

	r := Run(cfg)
	c := find(r, "log dir")
	if c == nil || c.Level != Warn || !strings.Contains(c.Message, "not writable") {
		t.Fatalf("unwritable log dir must warn, got %+v", c)
	}
}

func TestRunRedactsRepoURLCredentials(t *testing.T) {
	_, cfg := readyRoot(t)
	cfg.RepoURL = "https://user:sup3rsecret@github.com/example/scaletail"

	r := Run(cfg)
	c := find(r, "paths")
	if c == nil || c.Level != Info {
		t.Fatalf("paths summary must be reported as info, got %+v", c)
	}
	if strings.Contains(c.Message, "sup3rsecret") || strings.Contains(c.Message, "user:") {
		t.Fatalf("paths summary leaks credentials: %q", c.Message)
	}
	if !strings.Contains(c.Message, "https://github.com/example/scaletail") {
		t.Fatalf("paths summary must contain redacted URL, got %q", c.Message)
	}
}

func TestRunCatalogCheckReflectsServicesDir(t *testing.T) {
	_, cfg := readyRoot(t)
	r := Run(cfg)
	if c := find(r, "catalog"); c == nil || c.Level != OK {
		t.Fatalf("present services dir must report ok, got %+v", c)
	}

	absentCfg := testConfig(t.TempDir())
	r = Run(absentCfg)
	c := find(r, "catalog")
	if c == nil || c.Level != Warn || !strings.Contains(c.Message, "not found") {
		t.Fatalf("missing services dir must warn, got %+v", c)
	}
}

func TestRunWarnsForMissingDirectories(t *testing.T) {
	r := Run(testConfig(t.TempDir()))
	for _, label := range []string{"config dir", "ScaleTail parent", "deploy path", "log dir", "authkeys dir"} {
		c := find(r, label)
		if c == nil || c.Level != Warn || !strings.Contains(c.Message, "missing") {
			t.Fatalf("%s must warn when missing, got %+v", label, c)
		}
	}
}

func TestCheckPathEmptyPathFailsClosed(t *testing.T) {
	var r Result
	r.checkPath("label", "", true)
	if len(r.Checks) != 1 || r.Checks[0].Level != Fail || !strings.Contains(r.Checks[0].Message, "path is empty") {
		t.Fatalf("empty path must fail closed, got %+v", r.Checks)
	}
}

// probeWritable creates its probe with O_EXCL and removes it afterwards;
// the checked directory must come out exactly as it went in.
func TestProbeWritableCreatesAndRemovesProbe(t *testing.T) {
	dir := t.TempDir()
	if err := probeWritable(dir); err != nil {
		t.Fatalf("probeWritable on empty dir must succeed, got %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".tailarr-doctor-write-probe-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("probe file was not removed: %v", matches)
	}
}

func TestDoctorWarnsWithoutTun(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("TUN check runs on Linux only")
	}
	_, cfg := readyRoot(t)
	old := tunPath
	tunPath = filepath.Join(t.TempDir(), "missing-tun")
	t.Cleanup(func() { tunPath = old })
	if c := find(Run(cfg), "tun"); c == nil || c.Level != Warn {
		t.Fatalf("expected tun warning, got %+v", c)
	}
}

func TestDoctorNotesRemoveNeedsRoot(t *testing.T) {
	_, cfg := readyRoot(t)
	svc := filepath.Join(cfg.DeployPath, "web")
	if err := os.MkdirAll(svc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := euid
	t.Cleanup(func() { euid = old })

	euid = func() int { return 1000 }
	if c := find(Run(cfg), "privileges"); c == nil || c.Level != Info {
		t.Fatalf("expected privileges note for non-root, got %+v", c)
	}
	euid = func() int { return 0 }
	if find(Run(cfg), "privileges") != nil {
		t.Fatal("root needs no privileges note")
	}
}
