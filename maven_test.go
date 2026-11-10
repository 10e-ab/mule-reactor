package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAntPathMatch(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"*.properties", "app.properties", true},
		{"*.properties", "sub/app.properties", false}, // '*' does not cross '/'
		{"**/*.properties", "app.properties", true},   // '**/' matches zero dirs
		{"**/*.properties", "a/b/app.properties", true},
		{"config/**", "config/env/dev.yaml", true}, // bare '**' crosses dirs
		{"config/**", "other/dev.yaml", false},
		{"secret/**", "secret/creds.properties", true},
		{"a?c.txt", "abc.txt", true},
		{"a?c.txt", "a/c.txt", false}, // '?' does not match '/'
		{"a.txt", "a.txt", true},
		{"a.txt", "aatxt", false},    // '.' is literal
		{"a+b.txt", "a+b.txt", true}, // regexp metachars are literal
		{"a+b.txt", "aab.txt", false},
	}
	for _, c := range cases {
		if got := antPathMatch([]string{c.pattern}, c.path); got != c.want {
			t.Errorf("antPathMatch(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestJavaFormatTime(t *testing.T) {
	epoch := time.Unix(0, 0).UTC()
	cases := []struct {
		format string
		want   string
	}{
		{"yyyy-MM-dd'T'HH:mm:ss'Z'", "1970-01-01T00:00:00Z"},
		{"yyyy.MM.dd", "1970.01.01"},
		{"dd/MM/yy", "01/01/70"},
		{"HH:mm:ss.SSS", "00:00:00.000"},
		{"yyyy-MM-dd XXX", "1970-01-01 +00:00"},
		{"yyyy-MM-dd Z", "1970-01-01 +0000"},
		{"''", "'"},
		{"'at' HH", "at 00"},
		{"yyyy'", "1970'"}, // unterminated quote must not panic
	}
	for _, c := range cases {
		if got := javaFormatTime(epoch, c.format); got != c.want {
			t.Errorf("javaFormatTime(%q) = %q, want %q", c.format, got, c.want)
		}
	}
}

func TestMavenProperties(t *testing.T) {
	setOpts(t, defaultTestOpts())
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", `<?xml version="1.0"?>
<project>
  <groupId>g</groupId><artifactId>a</artifactId><version>1.0</version><name>app</name>
  <properties>
    <base>x</base>
    <derived>${base}-y</derived>
  </properties>
  <profiles>
    <profile>
      <id>on</id>
      <activation><activeByDefault>true</activeByDefault></activation>
      <properties><profprop>p1</profprop><base>override</base></properties>
    </profile>
    <profile>
      <id>off</id>
      <properties><hidden>nope</hidden></properties>
    </profile>
    <profile>
      <id>filecond</id>
      <activation><file><exists>marker.txt</exists></file></activation>
      <properties><marked>yes</marked></properties>
    </profile>
    <profile>
      <id>missingcond</id>
      <activation><file><missing>not-there.txt</missing></file></activation>
      <properties><unmarked>yes</unmarked></properties>
    </profile>
  </profiles>
</project>`)
	writeFile(t, dir+"/marker.txt", "")

	props, err := mavenProperties(dir)
	if err != nil {
		t.Fatal(err)
	}
	expect := map[string]string{
		"base":               "override", // active profile overrides project property
		"derived":            "override-y",
		"profprop":           "p1",
		"marked":             "yes", // file exists activation
		"unmarked":           "yes", // file missing activation
		"project.groupId":    "g",
		"project.artifactId": "a",
		"project.version":    "1.0",
		"project.name":       "app",
		"project.basedir":    dir,
	}
	for key, want := range expect {
		if props[key] != want {
			t.Errorf("props[%q] = %q, want %q", key, props[key], want)
		}
	}
	if _, ok := props["hidden"]; ok {
		t.Error("property from inactive profile should not be present")
	}
}

func TestMavenPropertiesParentFallback(t *testing.T) {
	setOpts(t, defaultTestOpts())
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", `<project>
  <artifactId>child</artifactId><name>child-app</name>
  <parent><groupId>pg</groupId><artifactId>parent</artifactId><version>9</version></parent>
</project>`)
	props, err := mavenProperties(dir)
	if err != nil {
		t.Fatal(err)
	}
	if props["project.groupId"] != "pg" || props["project.version"] != "9" {
		t.Errorf("parent fallback failed: groupId=%q version=%q", props["project.groupId"], props["project.version"])
	}
	if props["project.artifactId"] != "child" {
		t.Errorf("own artifactId must win over parent, got %q", props["project.artifactId"])
	}
}

const twoEntryPom = `<?xml version="1.0"?>
<project>
  <groupId>g</groupId><artifactId>a</artifactId><version>1.0</version><name>app</name>
  <properties><env>dev</env></properties>
  <build>
    <resources>
      <resource>
        <directory>src/main/resources</directory>
        <filtering>true</filtering>
        <includes><include>**/*.properties</include></includes>
        <excludes><exclude>secret/**</exclude></excludes>
      </resource>
      <resource>
        <directory>src/main/resources</directory>
        <filtering>false</filtering>
        <excludes><exclude>**/*.properties</exclude></excludes>
      </resource>
    </resources>
  </build>
</project>`

func TestFilteredResource(t *testing.T) {
	setOpts(t, defaultTestOpts())
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", twoEntryPom)
	res := dir + "/src/main/resources"

	cases := []struct {
		file string
		want bool
	}{
		{res + "/app.properties", true},           // filtered include
		{res + "/sub/deep.properties", true},      // '**/' crosses dirs
		{res + "/raw.yaml", false},                // only the filtering=false entry
		{res + "/secret/creds.properties", false}, // exclude wins over include
		{dir + "/src/main/mule/flow.xml", false},  // outside the resource dir
	}
	for _, c := range cases {
		got, err := filteredResource(c.file, dir)
		if err != nil {
			t.Fatalf("filteredResource(%q): %v", c.file, err)
		}
		if got != c.want {
			t.Errorf("filteredResource(%q) = %v, want %v", c.file, got, c.want)
		}
	}
}

func TestFilteredResourceDirectoryWithTokens(t *testing.T) {
	setOpts(t, defaultTestOpts())
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", `<project>
  <groupId>g</groupId><artifactId>a</artifactId><version>1.0</version><name>app</name>
  <properties><res.dir>src/main/resources</res.dir></properties>
  <build>
    <resources>
      <resource>
        <directory>${project.basedir}/src/main/resources</directory>
        <filtering>true</filtering>
        <includes><include>**/*.properties</include></includes>
      </resource>
      <resource>
        <directory>${basedir}/${res.dir}</directory>
        <filtering>true</filtering>
        <includes><include>**/*.yaml</include></includes>
      </resource>
    </resources>
  </build>
</project>`)
	res := dir + "/src/main/resources"

	cases := []struct {
		file string
		want bool
	}{
		{res + "/app.properties", true}, // via ${project.basedir}
		{res + "/config.yaml", true},    // via ${basedir} + a custom property
		{res + "/other.txt", false},     // matched by neither include
	}
	for _, c := range cases {
		got, err := filteredResource(c.file, dir)
		if err != nil {
			t.Fatalf("filteredResource(%q): %v", c.file, err)
		}
		if got != c.want {
			t.Errorf("filteredResource(%q) = %v, want %v", c.file, got, c.want)
		}
	}
}

func TestFilteredResourceNoBuildSection(t *testing.T) {
	setOpts(t, defaultTestOpts())
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", `<project><name>app</name></project>`)
	got, err := filteredResource(dir+"/src/main/resources/a.properties", dir)
	if err != nil || got {
		t.Errorf("pom without build section: got (%v, %v), want (false, nil)", got, err)
	}
}

func TestFilterMavenTokens(t *testing.T) {
	setOpts(t, defaultTestOpts())
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", twoEntryPom)

	got, err := filterMavenTokens("a=${env}\nb=${no.such}\nv=${project.version}\nts=${maven.build.timestamp}\n", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	want := "a=dev\nb=${no.such}\nv=1.0\nts=1970-01-01T00:00:00Z\n"
	if got != want {
		t.Errorf("filterMavenTokens = %q, want %q", got, want)
	}
}

func TestFilterMavenTokensUserName(t *testing.T) {
	setOpts(t, defaultTestOpts())
	t.Setenv("USER", "tester")
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", twoEntryPom)
	got, err := filterMavenTokens("u=${user.name}", dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "u=tester" {
		t.Errorf("got %q, want u=tester", got)
	}
}

// A hot sync is not a build, so the build timestamp must survive it: these
// cover what is carried over from the deployed copy and what falls back to the
// epoch sentinel.

const builtAtSource = "built.at=${maven.build.timestamp}\nkey=new\n"

// carryFixture writes a pom and a deployed copy, and returns the project dir
// and the path to the deployed copy.
func carryFixture(t *testing.T, deployedContent string) (string, string) {
	t.Helper()
	setOpts(t, defaultTestOpts())
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", twoEntryPom)
	deployed := dir + "/deployed/app.properties"
	writeFile(t, deployed, deployedContent)
	return dir, deployed
}

func TestFilterMavenTokensCarriesDeployedBuildTimestamp(t *testing.T) {
	dir, deployed := carryFixture(t, "built.at=2026-09-07T10:12:33Z\nkey=old\n")

	got, err := filterMavenTokens(builtAtSource, dir, deployed)
	if err != nil {
		t.Fatal(err)
	}
	want := "built.at=2026-09-07T10:12:33Z\nkey=new\n"
	if got != want {
		t.Errorf("filterMavenTokens = %q, want %q", got, want)
	}
}

// The value written is the value found, so filtering the same source again
// against the copy just deployed must produce the same bytes — otherwise every
// batch would see a difference and redeploy in a loop.
func TestFilterMavenTokensCarryForwardIsAFixedPoint(t *testing.T) {
	dir, deployed := carryFixture(t, "built.at=2026-09-07T10:12:33Z\nkey=old\n")

	first, err := filterMavenTokens(builtAtSource, dir, deployed)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, deployed, first)
	second, err := filterMavenTokens(builtAtSource, dir, deployed)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Errorf("second pass = %q, want the first pass unchanged %q", second, first)
	}
}

func TestFilterMavenTokensSentinelWithoutDeployedCopy(t *testing.T) {
	setOpts(t, defaultTestOpts())
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", twoEntryPom)

	for name, deployed := range map[string]string{
		"no destination known": "",
		"first deploy":         dir + "/deployed/app.properties",
	} {
		got, err := filterMavenTokens(builtAtSource, dir, deployed)
		if err != nil {
			t.Fatal(err)
		}
		want := "built.at=1970-01-01T00:00:00Z\nkey=new\n"
		if got != want {
			t.Errorf("%s: filterMavenTokens = %q, want %q", name, got, want)
		}
	}
}

func TestFilterMavenTokensSentinelWhenNothingCanBeCarried(t *testing.T) {
	cases := map[string]struct {
		source, deployed string
	}{
		// The line holding the token is gone from the deployed copy
		"line no longer there": {builtAtSource, "other=1\n"},
		// Carrying this back would put an unresolved token into the deployed
		// copy, which is what filtering exists to prevent
		"deployed copy was never filtered": {builtAtSource, "built.at=${maven.build.timestamp}\n"},
		// Nothing there to carry
		"deployed value is empty": {builtAtSource, "built.at=\n"},
		// Two occurrences that disagree: neither is knowably the build's
		"occurrences disagree": {
			"a=${maven.build.timestamp}\nb=${maven.build.timestamp}\n",
			"a=2026-09-07T10:12:33Z\nb=2026-01-01T00:00:00Z\n",
		},
	}
	for name, tc := range cases {
		dir, deployed := carryFixture(t, tc.deployed)
		got, err := filterMavenTokens(tc.source, dir, deployed)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "1970-01-01T00:00:00Z") {
			t.Errorf("%s: filterMavenTokens = %q, want the epoch sentinel", name, got)
		}
	}
}

// Agreeing occurrences are one build's timestamp and are all carried over.
func TestFilterMavenTokensCarriesAgreeingOccurrences(t *testing.T) {
	dir, deployed := carryFixture(t, "a=2026-09-07T10:12:33Z\nb=2026-09-07T10:12:33Z\n")

	got, err := filterMavenTokens("a=${maven.build.timestamp}\nb=${maven.build.timestamp}\n", dir, deployed)
	if err != nil {
		t.Fatal(err)
	}
	want := "a=2026-09-07T10:12:33Z\nb=2026-09-07T10:12:33Z\n"
	if got != want {
		t.Errorf("filterMavenTokens = %q, want %q", got, want)
	}
}

// The token need not sit in a key=value line: the whole line around it is the
// pattern, so XML and DataWeave carry over just as well.
func TestFilterMavenTokensCarriesTimestampInMarkup(t *testing.T) {
	dir, deployed := carryFixture(t, "  <builtAt>2026-09-07T10:12:33Z</builtAt>\n")

	got, err := filterMavenTokens("  <builtAt>${maven.build.timestamp}</builtAt>\n", dir, deployed)
	if err != nil {
		t.Fatal(err)
	}
	want := "  <builtAt>2026-09-07T10:12:33Z</builtAt>\n"
	if got != want {
		t.Errorf("filterMavenTokens = %q, want %q", got, want)
	}
}

func TestWithSourceFileCarriesDeployedBuildTimestamp(t *testing.T) {
	setOpts(t, defaultTestOpts())
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", twoEntryPom)
	file := dir + "/src/main/resources/app.properties"
	writeFile(t, file, builtAtSource)
	deployed := dir + "/deployed/app.properties"
	writeFile(t, deployed, "built.at=2026-09-07T10:12:33Z\nkey=old\n")

	called := false
	withSourceFile(file, dir, deployed, func(source string) {
		called = true
		want := "built.at=2026-09-07T10:12:33Z\nkey=new\n"
		if got := readFile(t, source); got != want {
			t.Errorf("filtered content = %q, want %q", got, want)
		}
	})
	if !called {
		t.Fatal("fn was not called")
	}
}

func TestDynamicMavenPropertyGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "Tester"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	commit, ok := dynamicMavenProperty("git.commit.id", dir, nil)
	if !ok || len(commit) != 40 {
		t.Errorf("git.commit.id = (%q, %v), want 40-char sha", commit, ok)
	}
	dirty, ok := dynamicMavenProperty("git.dirty", dir, nil)
	if !ok || dirty != "false" {
		t.Errorf("git.dirty on clean repo = (%q, %v), want false", dirty, ok)
	}
	writeFile(t, dir+"/junk.txt", "x")
	dirty, ok = dynamicMavenProperty("git.dirty", dir, nil)
	if !ok || dirty != "true" {
		t.Errorf("git.dirty on dirty repo = (%q, %v), want true", dirty, ok)
	}
}

func TestDynamicMavenPropertyGitPushed(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	origin := t.TempDir()
	git(origin, "init", "-q", "--bare")

	dir := t.TempDir()
	git(dir, "init", "-q")
	git(dir, "config", "user.email", "t@example.com")
	git(dir, "config", "user.name", "Tester")
	git(dir, "commit", "-q", "--allow-empty", "-m", "init")

	pushed, ok := dynamicMavenProperty("git.pushed", dir, nil)
	if !ok || pushed != "false" {
		t.Errorf("git.pushed with no remote = (%q, %v), want false", pushed, ok)
	}

	git(dir, "remote", "add", "origin", origin)
	git(dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	git(dir, "fetch", "-q", "origin")

	// On origin even though this branch has no upstream, which is exactly the
	// case a branch ahead-count reports wrongly
	pushed, ok = dynamicMavenProperty("git.pushed", dir, nil)
	if !ok || pushed != "true" {
		t.Errorf("git.pushed after push = (%q, %v), want true", pushed, ok)
	}

	git(dir, "commit", "-q", "--allow-empty", "-m", "local only")
	pushed, ok = dynamicMavenProperty("git.pushed", dir, nil)
	if !ok || pushed != "false" {
		t.Errorf("git.pushed with unpushed commit = (%q, %v), want false", pushed, ok)
	}
}

func TestDynamicMavenPropertyGitUnreleased(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	// The newest tag is chosen by commit date, so every commit gets its own
	// second; made within the same one, the tags would tie and either could win
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	git := func(args ...string) {
		t.Helper()
		clock = clock.Add(time.Second)
		date := clock.Format(time.RFC3339)
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	commit := func(message string) { git("commit", "-q", "--allow-empty", "-m", message) }
	want := func(expected, why string) {
		t.Helper()
		got, ok := dynamicMavenProperty("git.unreleased", dir, nil)
		if !ok || got != expected {
			t.Errorf("git.unreleased %s = (%q, %v), want %q", why, got, ok, expected)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "Tester")
	commit("init")
	want("unknown", "with no tags")

	commit("[maven-release-plugin] prepare release 1.0.0")
	git("tag", "1.0.0")
	commit("[maven-release-plugin] prepare for next development iteration")
	want("0", "right after a release")

	// Forked before the next release, so its first commit ships in 1.0.1
	// while the branch never sees that tag
	git("checkout", "-q", "-b", "feature")
	commit("shipped in 1.0.1")
	git("checkout", "-q", "main")
	git("merge", "-q", "--ff-only", "feature")
	commit("[maven-release-plugin] prepare release 1.0.1")
	git("tag", "1.0.1")
	commit("[maven-release-plugin] prepare for next development iteration")
	git("checkout", "-q", "feature")
	commit("feature work")
	want("1", "on a branch forked before the latest release")

	git("checkout", "-q", "main")
	commit("on main")
	git("merge", "-q", "--no-ff", "-m", "merge feature", "feature")
	want("2", "with a merge commit")
}

func TestFilteredResourcesToRecheck(t *testing.T) {
	setOpts(t, defaultTestOpts())
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", twoEntryPom)
	props := dir + "/src/main/resources/app.properties"
	writeFile(t, props, "sha=${git.commit.id}\n")
	writeFile(t, dir+"/src/main/resources/plain.txt", "not filtered\n")
	writeFile(t, dir+"/src/main/resources/secret/hidden.properties", "excluded\n")
	writeFile(t, dir+"/src/main/mule/api.xml", "<mule/>\n")

	// An edit anywhere in the project must pull in its filtered resources, because
	// their git-derived tokens can have gone stale without a file event of their own
	got := filteredResourcesToRecheck(map[string]bool{dir + "/src/main/mule/api.xml": true})
	if len(got) != 1 || got[0] != props {
		t.Errorf("got %v, want [%s]", got, props)
	}

	// Files the batch already synced are not synced twice
	got = filteredResourcesToRecheck(map[string]bool{props: true})
	if len(got) != 0 {
		t.Errorf("already-handled file should not be rechecked, got %v", got)
	}

	// A batch touching nothing in a project must not drag that project in
	if got = filteredResourcesToRecheck(map[string]bool{}); len(got) != 0 {
		t.Errorf("empty batch should recheck nothing, got %v", got)
	}

	off := defaultTestOpts()
	off.ResourceFiltering = false
	setOpts(t, off)
	if got = filteredResourcesToRecheck(map[string]bool{dir + "/src/main/mule/api.xml": true}); got != nil {
		t.Errorf("--no-resource-filtering should recheck nothing, got %v", got)
	}
}

func TestWithSourceFileFiltersAndCleansUp(t *testing.T) {
	setOpts(t, defaultTestOpts())
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", twoEntryPom)
	file := dir + "/src/main/resources/app.properties"
	writeFile(t, file, "env=${env}\n")
	if err := os.Chmod(file, 0o640); err != nil {
		t.Fatal(err)
	}

	var tempPath string
	called := false
	withSourceFile(file, dir, "", func(source string) {
		called = true
		tempPath = source
		if source == file {
			t.Error("filtered resource should be handed over as a temp file")
		}
		if got := readFile(t, source); got != "env=dev\n" {
			t.Errorf("filtered content = %q, want env=dev", got)
		}
		if info, err := os.Stat(source); err != nil || info.Mode().Perm() != 0o640 {
			t.Errorf("temp file must carry the source permissions, got %v (%v)", info.Mode().Perm(), err)
		}
	})
	if !called {
		t.Fatal("fn was not called")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Errorf("temp file %s should be deleted after withSourceFile", tempPath)
	}
}

func TestWithSourceFileSkipsOnBrokenPom(t *testing.T) {
	setOpts(t, defaultTestOpts())
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", "<project><broken")
	file := dir + "/src/main/resources/app.properties"
	writeFile(t, file, "env=${env}\n")

	called := false
	withSourceFile(file, dir, "", func(string) { called = true })
	if called {
		t.Error("sync must be skipped when the pom cannot be parsed")
	}
}

func TestWithSourceFileRawWhenFilteringDisabled(t *testing.T) {
	o := defaultTestOpts()
	o.ResourceFiltering = false
	setOpts(t, o)
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", twoEntryPom)
	file := dir + "/src/main/resources/app.properties"
	writeFile(t, file, "env=${env}\n")

	withSourceFile(file, dir, "", func(source string) {
		if source != file {
			t.Errorf("with filtering disabled the original file should be synced, got %q", source)
		}
	})
}

func TestWithSourceFileRawOnInvalidUTF8(t *testing.T) {
	setOpts(t, defaultTestOpts())
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", twoEntryPom)
	file := dir + "/src/main/resources/bin.properties"
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte{0xff, 0xfe, 0x00, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}
	withSourceFile(file, dir, "", func(source string) {
		if source != file {
			t.Errorf("non-UTF-8 content should be synced raw, got %q", source)
		}
	})
}

func TestProfileActiveConditions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/present.txt", "")
	parse := func(xml string) *XMLNode {
		root, err := parseXML([]byte(xml))
		if err != nil {
			t.Fatal(err)
		}
		return root
	}
	cases := []struct {
		name string
		xml  string
		want bool
	}{
		{"no activation", `<profile><id>x</id></profile>`, false},
		{"activeByDefault", `<profile><activation><activeByDefault>true</activeByDefault></activation></profile>`, true},
		{"activeByDefault false", `<profile><activation><activeByDefault>false</activeByDefault></activation></profile>`, false},
		{"file exists", `<profile><activation><file><exists>present.txt</exists></file></activation></profile>`, true},
		{"file exists missing", `<profile><activation><file><exists>absent.txt</exists></file></activation></profile>`, false},
		{"file missing", `<profile><activation><file><missing>absent.txt</missing></file></activation></profile>`, true},
	}
	for _, c := range cases {
		if got := profileActive(parse(c.xml), dir); got != c.want {
			t.Errorf("%s: profileActive = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestUniqueStrings(t *testing.T) {
	got := uniqueStrings([]string{"b", "a", "b", "c", "a"})
	if strings.Join(got, ",") != "b,a,c" {
		t.Errorf("uniqueStrings = %v", got)
	}
}
