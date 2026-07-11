package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pomStateFromString(t *testing.T, content string) pomState {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", content)
	state, err := pomStateFor(dir + "/pom.xml")
	if err != nil {
		t.Fatal(err)
	}
	return state
}

const basePom = `<project>
  <groupId>g</groupId><artifactId>a</artifactId><version>1.0</version><name>app</name>
  <parent><groupId>pg</groupId><artifactId>parent</artifactId><version>2</version></parent>
  <properties><env>dev</env></properties>
  <dependencies>
    <dependency><groupId>org.mule</groupId><artifactId>core</artifactId><version>4.4.0</version></dependency>
  </dependencies>
  <build>
    <resources>
      <resource><directory>src/main/resources</directory><filtering>true</filtering></resource>
    </resources>
  </build>
</project>`

func TestPomRebuildWorthy(t *testing.T) {
	setOpts(t, defaultTestOpts())
	base := pomStateFromString(t, basePom)

	t.Run("formatting-only edit is not rebuild-worthy", func(t *testing.T) {
		reformatted := pomStateFromString(t, "<project>\n\n  <groupId>g</groupId><artifactId>a</artifactId><version>1.0</version><name>app</name>\n  <parent><groupId>pg</groupId><artifactId>parent</artifactId><version>2</version></parent>\n  <properties><env>dev</env></properties>\n  <dependencies>\n    <dependency>\n        <groupId>org.mule</groupId>\n        <artifactId>core</artifactId>\n        <version>4.4.0</version>\n    </dependency>\n  </dependencies>\n  <build><resources><resource><directory>src/main/resources</directory><filtering>true</filtering></resource></resources></build>\n</project>\n")
		if pomRebuildWorthy(base, reformatted) {
			t.Error("reformatting must not trigger a rebuild")
		}
	})

	t.Run("dependency element order is irrelevant", func(t *testing.T) {
		reordered := pomStateFromString(t, `<project>
  <groupId>g</groupId><artifactId>a</artifactId><version>1.0</version><name>app</name>
  <parent><groupId>pg</groupId><artifactId>parent</artifactId><version>2</version></parent>
  <properties><env>dev</env></properties>
  <dependencies>
    <dependency><version>4.4.0</version><artifactId>core</artifactId><groupId>org.mule</groupId></dependency>
  </dependencies>
  <build>
    <resources>
      <resource><directory>src/main/resources</directory><filtering>true</filtering></resource>
    </resources>
  </build>
</project>`)
		if base.hash != reordered.hash {
			t.Error("dependency child order must not change the dependency hash")
		}
	})

	t.Run("dependency version change is rebuild-worthy", func(t *testing.T) {
		bumped := pomStateFromString(t, replaceOnce(t, basePom, "4.4.0", "4.5.0"))
		if !pomRebuildWorthy(base, bumped) {
			t.Error("dependency change must trigger a rebuild")
		}
	})

	t.Run("parent version change is rebuild-worthy", func(t *testing.T) {
		bumped := pomStateFromString(t, replaceOnce(t, basePom, "<version>2</version>", "<version>3</version>"))
		if !pomRebuildWorthy(base, bumped) {
			t.Error("parent change must trigger a rebuild")
		}
	})

	t.Run("property change is rebuild-worthy with filtering on", func(t *testing.T) {
		changed := pomStateFromString(t, replaceOnce(t, basePom, "<env>dev</env>", "<env>prod</env>"))
		if !pomRebuildWorthy(base, changed) {
			t.Error("property change must trigger a rebuild when filtering is on")
		}
	})

	t.Run("property change is ignored with filtering off", func(t *testing.T) {
		o := defaultTestOpts()
		o.ResourceFiltering = false
		setOpts(t, o)
		changed := pomStateFromString(t, replaceOnce(t, basePom, "<env>dev</env>", "<env>prod</env>"))
		if pomRebuildWorthy(base, changed) {
			t.Error("property change must not trigger a rebuild with --no-resource-filtering")
		}
	})

	t.Run("resources section change is rebuild-worthy", func(t *testing.T) {
		setOpts(t, defaultTestOpts())
		changed := pomStateFromString(t, replaceOnce(t, basePom, "<filtering>true</filtering>", "<filtering>false</filtering>"))
		if !pomRebuildWorthy(base, changed) {
			t.Error("resources change must trigger a rebuild when filtering is on")
		}
	})
}

func replaceOnce(t *testing.T, s, old, new string) string {
	t.Helper()
	if !strings.Contains(s, old) {
		t.Fatalf("substring %q not found", old)
	}
	return strings.Replace(s, old, new, 1)
}

func TestInitializePomState(t *testing.T) {
	setOpts(t, defaultTestOpts())
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", basePom)
	writeFile(t, dir+"/proj/pom.xml", basePom)
	writeFile(t, dir+"/.hidden/pom.xml", basePom)
	writeFile(t, dir+"/broken/pom.xml", "<project><broken")
	writeFile(t, dir+"/too/deep/pom.xml", basePom)

	states := initializePomState(dir)
	if len(states) != 2 {
		keys := make([]string, 0, len(states))
		for k := range states {
			keys = append(keys, k)
		}
		t.Errorf("expected root + one-level-down poms only, got %v", keys)
	}
}

func TestBuildCommand(t *testing.T) {
	// control PATH so the mvnd detection is deterministic
	bin := t.TempDir()
	t.Setenv("PATH", bin)

	cmd, display := buildCommand()
	if cmd.Args[0] != "mvn" || len(cmd.Args) != 4 || display != defaultBuildCommand {
		t.Errorf("default build command = %v, displayed as %q", cmd.Args, display)
	}

	// with mvnd on PATH the daemon is preferred
	writeFile(t, bin+"/mvnd", "#!/bin/sh\n")
	if err := os.Chmod(bin+"/mvnd", 0o755); err != nil {
		t.Fatal(err)
	}
	cmd, display = buildCommand()
	if cmd.Args[0] != "mvnd" || display != "mvnd clean package -DskipTests" {
		t.Errorf("mvnd on PATH should be preferred: %v, displayed as %q", cmd.Args, display)
	}

	// an explicit build command always wins
	t.Setenv("MULE_REACTOR_BUILD_COMMAND", "echo custom && true")
	cmd, display = buildCommand()
	if cmd.Args[0] != "sh" || cmd.Args[1] != "-c" || cmd.Args[2] != "echo custom && true" || display != "echo custom && true" {
		t.Errorf("custom build command = %v, displayed as %q", cmd.Args, display)
	}
}

func TestSelectBuildJar(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/pom.xml", `<project><artifactId>my-artifact</artifactId><name>My Nice Name</name></project>`)

	if _, err := selectBuildJar(dir, dir+"/pom.xml", "My Nice Name"); err == nil {
		t.Error("no jar in target/ must be an error")
	}

	// the jar is named by artifactId, not by the pom <name>
	writeFile(t, dir+"/target/my-artifact-1.0-mule-application.jar", "old")
	jar, err := selectBuildJar(dir, dir+"/pom.xml", "My Nice Name")
	if err != nil || filepath.Base(jar) != "my-artifact-1.0-mule-application.jar" {
		t.Errorf("artifactId-named jar should be found: (%q, %v)", jar, err)
	}

	// several versions (a build command without 'clean'): newest wins
	writeFile(t, dir+"/target/my-artifact-1.1-mule-application.jar", "new")
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(dir+"/target/my-artifact-1.0-mule-application.jar", past, past); err != nil {
		t.Fatal(err)
	}
	jar, err = selectBuildJar(dir, dir+"/pom.xml", "My Nice Name")
	if err != nil || filepath.Base(jar) != "my-artifact-1.1-mule-application.jar" {
		t.Errorf("newest jar should win: (%q, %v)", jar, err)
	}
}
