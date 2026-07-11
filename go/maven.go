package main

// Maven resource filtering
//
// A pom.xml can declare <build><resources><resource> entries with
// <filtering>true</filtering>, which makes Maven replace ${...} tokens in the
// matched resource files at build time. Since we sync the raw files from
// src/main/resources, such tokens would reach the runtime unresolved and break
// Mule property resolution. For files covered by a filtered resource we mimic
// Maven: substitute ${...} tokens before syncing, using the pom properties,
// the project coordinates and live equivalents of build-time values
// (git commit/dirty as injected by git-commit-id-maven-plugin, and
// ${maven.build.timestamp}). Unknown tokens are left as-is, like Maven does.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var mavenTokenRegex = regexp.MustCompile(`\$\{([^}]+)\}`)

type pomCacheEntry struct {
	mtime time.Time
	root  *XMLNode
}

// Parsed poms memoized by path+mtime: file events fire on every save and
// would otherwise re-read and re-parse the pom each time
var (
	pomCache   = map[string]pomCacheEntry{}
	pomCacheMu sync.Mutex
)

// pomDocument returns (nil, nil) when the pom does not exist, and an error
// when it exists but cannot be read or parsed (e.g. mid-edit)
func pomDocument(pomFile string) (*XMLNode, error) {
	info, err := os.Stat(pomFile)
	if err != nil {
		return nil, nil
	}
	pomCacheMu.Lock()
	defer pomCacheMu.Unlock()
	if entry, ok := pomCache[pomFile]; ok && entry.mtime.Equal(info.ModTime()) {
		return entry.root, nil
	}
	data, err := os.ReadFile(pomFile)
	if err != nil {
		return nil, err
	}
	root, err := parseXML(data)
	if err != nil {
		return nil, err
	}
	pomCache[pomFile] = pomCacheEntry{mtime: info.ModTime(), root: root}
	return root, nil
}

type resourceDef struct {
	directory string
	includes  []string
	excludes  []string
}

func filteredResourceDefs(pomFile string) ([]resourceDef, error) {
	root, err := pomDocument(pomFile)
	if err != nil || root == nil {
		return nil, err
	}
	var defs []resourceDef
	resources := root.find("build", "resources")
	for _, resource := range resources.childrenNamed("resource") {
		if resource.childText("filtering") != "true" {
			continue
		}
		directory := resource.childText("directory")
		if directory == "" {
			continue
		}
		def := resourceDef{directory: directory}
		for _, inc := range resource.find("includes").childrenNamed("include") {
			def.includes = append(def.includes, strings.TrimSpace(inc.Text))
		}
		for _, exc := range resource.find("excludes").childrenNamed("exclude") {
			def.excludes = append(def.excludes, strings.TrimSpace(exc.Text))
		}
		defs = append(defs, def)
	}
	return defs, nil
}

// antPathMatch matches with ant-style semantics: '*' does not cross '/',
// '**/' matches zero or more directories (so '**/x' also matches a
// root-level x), and a bare '**' matches anything including '/'
func antPathMatch(patterns []string, relativePath string) bool {
	for _, pattern := range patterns {
		if antPatternToRegexp(pattern).MatchString(relativePath) {
			return true
		}
	}
	return false
}

var (
	antRegexpCache   = map[string]*regexp.Regexp{}
	antRegexpCacheMu sync.Mutex
)

func antPatternToRegexp(pattern string) *regexp.Regexp {
	antRegexpCacheMu.Lock()
	defer antRegexpCacheMu.Unlock()
	if re, ok := antRegexpCache[pattern]; ok {
		return re
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); {
		switch {
		case strings.HasPrefix(pattern[i:], "**/"):
			b.WriteString(`(?:[^/]+/)*`)
			i += 3
		case strings.HasPrefix(pattern[i:], "**"):
			for i < len(pattern) && pattern[i] == '*' {
				i++
			}
			b.WriteString(`.*`)
		case pattern[i] == '*':
			b.WriteString(`[^/]*`)
			i++
		case pattern[i] == '?':
			b.WriteString(`[^/]`)
			i++
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
			i++
		}
	}
	b.WriteString("$")
	re := regexp.MustCompile(b.String())
	antRegexpCache[pattern] = re
	return re
}

func filteredResource(file, projectRoot string) (bool, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return false, err
	}
	abs = filepath.ToSlash(abs)
	defs, err := filteredResourceDefs(projectRoot + "/pom.xml")
	if err != nil {
		return false, err
	}
	// A resource <directory> may itself contain ${...} tokens
	// (e.g. ${project.basedir}/src/main/resources); Maven interpolates them
	// before use, so we must too or the prefix match silently never hits
	var properties map[string]string
	for _, def := range defs {
		directory := def.directory
		if strings.Contains(directory, "${") {
			if properties == nil {
				if properties, err = mavenProperties(projectRoot); err != nil {
					return false, err
				}
			}
			directory = resolveTokens(directory, properties)
			if strings.Contains(directory, "${") {
				// e.g. a property defined only in the parent pom, which is
				// not available on disk — better to say so than to silently
				// sync the resource unfiltered
				fmt.Printf("WARNING: cannot resolve ${...} in resource directory %s, skipping this filtering entry\n", def.directory)
				continue
			}
		}
		resourceDir := ensureTrailingSlash(filepath.ToSlash(filepath.Clean(resolveAgainst(projectRoot, directory))))
		if !strings.HasPrefix(abs, resourceDir) {
			continue
		}
		relativePath := strings.TrimPrefix(abs, resourceDir)
		included := len(def.includes) == 0 || antPathMatch(def.includes, relativePath)
		if included && !antPathMatch(def.excludes, relativePath) {
			return true, nil
		}
	}
	return false, nil
}

// Only activation conditions we can evaluate locally are supported:
// activeByDefault and file exists/missing. Profiles activated via -P flags,
// settings.xml, JDK, OS or property conditions are not considered.
func profileActive(profile *XMLNode, projectRoot string) bool {
	activation := profile.child("activation")
	if activation == nil {
		return false
	}
	if activation.childText("activeByDefault") == "true" {
		return true
	}
	if fileCondition := activation.child("file"); fileCondition != nil {
		exists := fileCondition.childText("exists")
		missing := fileCondition.childText("missing")
		if exists != "" {
			return fileExists(resolveAgainst(projectRoot, exists))
		}
		if missing != "" {
			return !fileExists(resolveAgainst(projectRoot, missing))
		}
	}
	return false
}

func resolveAgainst(base, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mavenProperties(projectRoot string) (map[string]string, error) {
	root, err := pomDocument(projectRoot + "/pom.xml")
	if err != nil {
		return nil, err
	}
	properties := map[string]string{}
	if root == nil {
		return properties, nil
	}
	addProperties(root.child("properties"), properties)
	// Properties from active profiles override the project-level ones
	for _, profile := range root.child("profiles").childrenNamed("profile") {
		if !profileActive(profile, projectRoot) {
			continue
		}
		if opts.Verbose {
			fmt.Printf("Maven filtering: including properties from active profile '%s'\n", profile.childText("id"))
		}
		addProperties(profile.child("properties"), properties)
	}
	// Project coordinates, falling back to the parent pom values when inherited
	for _, field := range []string{"groupId", "artifactId", "version", "name"} {
		value := root.childText(field)
		if value == "" {
			value = root.find("parent").childText(field)
		}
		if value != "" {
			properties["project."+field] = value
		}
	}
	properties["project.basedir"] = projectRoot
	// Deprecated Maven alias for project.basedir, still common in poms
	properties["basedir"] = projectRoot
	// Pom properties can reference each other, resolve a few levels of nesting
	for pass := 0; pass < 3; pass++ {
		for key, value := range properties {
			properties[key] = resolveTokens(value, properties)
		}
	}
	return properties, nil
}

// resolveTokens substitutes ${...} tokens from props, leaving unknown
// tokens untouched
func resolveTokens(s string, props map[string]string) string {
	return mavenTokenRegex.ReplaceAllStringFunc(s, func(token string) string {
		if value, ok := props[token[2:len(token)-1]]; ok {
			return value
		}
		return token
	})
}

func addProperties(node *XMLNode, into map[string]string) {
	if node == nil {
		return
	}
	for i := range node.Children {
		into[node.Children[i].XMLName.Local] = node.Children[i].Text
	}
}

func gitOutput(projectRoot string, args ...string) (string, bool) {
	cmd := exec.Command("git", append([]string{"-C", projectRoot}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// Live equivalents of values that Maven plugins compute at build time
func dynamicMavenProperty(key, projectRoot string, properties map[string]string) (string, bool) {
	switch key {
	case "maven.build.timestamp":
		// Deliberately a fixed sentinel, not time.Now(): it keeps the filtered
		// output deterministic (so unchanged files diff as identical and don't
		// redeploy) and honestly signals that the file was hot-synced, not built
		javaFormat := properties["maven.build.timestamp.format"]
		if javaFormat == "" {
			javaFormat = "yyyy-MM-dd'T'HH:mm:ss'Z'"
		}
		return javaFormatTime(time.Unix(0, 0).UTC(), javaFormat), true
	case "git.commit.id", "git.commit.id.full":
		return gitOutput(projectRoot, "rev-parse", "HEAD")
	case "git.commit.id.abbrev":
		return gitOutput(projectRoot, "rev-parse", "--short", "HEAD")
	case "git.branch":
		return gitOutput(projectRoot, "rev-parse", "--abbrev-ref", "HEAD")
	case "git.dirty":
		changes, ok := gitOutput(projectRoot, "status", "--porcelain")
		if !ok {
			return "", false
		}
		return strconv.FormatBool(changes != ""), true
	case "user.name":
		// Maven resolves ${user.name} from the JVM system property
		if v := os.Getenv("USER"); v != "" {
			return v, true
		}
		if v := os.Getenv("USERNAME"); v != "" {
			return v, true
		}
	}
	return "", false
}

var javaFormatTokenRegex = regexp.MustCompile(`(?s)'[^']*'|y+|M+|d+|E+|a|H+|h+|m+|s+|S+|X+|Z+|z+|.`)

// javaFormatTime formats t according to a Java SimpleDateFormat pattern,
// covering the tokens commonly used in maven.build.timestamp.format. Unknown
// tokens are emitted unchanged.
func javaFormatTime(t time.Time, javaFormat string) string {
	var b strings.Builder
	for _, token := range javaFormatTokenRegex.FindAllString(javaFormat, -1) {
		switch {
		// A lone unterminated quote falls through to the default case
		case strings.HasPrefix(token, "'") && len(token) >= 2 && strings.HasSuffix(token, "'"):
			inner := token[1 : len(token)-1]
			if inner == "" {
				b.WriteString("'")
			} else {
				b.WriteString(inner)
			}
		case token == "yyyy" || token == "yyy":
			fmt.Fprintf(&b, "%04d", t.Year())
		case token == "yy" || token == "y":
			fmt.Fprintf(&b, "%02d", t.Year()%100)
		case token == "MMMM":
			b.WriteString(t.Month().String())
		case token == "MMM":
			b.WriteString(t.Month().String()[:3])
		case token == "MM" || token == "M":
			fmt.Fprintf(&b, "%02d", int(t.Month()))
		case token == "dd" || token == "d":
			fmt.Fprintf(&b, "%02d", t.Day())
		case token == "EEEE":
			b.WriteString(t.Weekday().String())
		case token[0] == 'E':
			b.WriteString(t.Weekday().String()[:3])
		case token == "a":
			if t.Hour() < 12 {
				b.WriteString("AM")
			} else {
				b.WriteString("PM")
			}
		case token == "HH" || token == "H":
			fmt.Fprintf(&b, "%02d", t.Hour())
		case token == "hh" || token == "h":
			hour := t.Hour() % 12
			if hour == 0 {
				hour = 12
			}
			fmt.Fprintf(&b, "%02d", hour)
		case token == "mm" || token == "m":
			fmt.Fprintf(&b, "%02d", t.Minute())
		case token == "ss" || token == "s":
			fmt.Fprintf(&b, "%02d", t.Second())
		case token[0] == 'S':
			fmt.Fprintf(&b, "%03d", t.Nanosecond()/1e6)
		case token == "XXX":
			b.WriteString(zoneOffset(t, true))
		case token[0] == 'X' || token[0] == 'Z' || token[0] == 'z':
			b.WriteString(zoneOffset(t, false))
		default:
			b.WriteString(token)
		}
	}
	return b.String()
}

func zoneOffset(t time.Time, colon bool) string {
	_, offset := t.Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	hours := offset / 3600
	minutes := (offset % 3600) / 60
	if colon {
		return fmt.Sprintf("%s%02d:%02d", sign, hours, minutes)
	}
	return fmt.Sprintf("%s%02d%02d", sign, hours, minutes)
}

func filterMavenTokens(content, projectRoot string) (string, error) {
	properties, err := mavenProperties(projectRoot)
	if err != nil {
		return "", err
	}
	// Dynamic values can spawn a git subprocess, resolve each key at most once
	dynamicValues := map[string]*string{}
	var substituted, unresolved []string
	filtered := mavenTokenRegex.ReplaceAllStringFunc(content, func(token string) string {
		key := token[2 : len(token)-1]
		value, cached := dynamicValues[key]
		if !cached {
			if v, ok := dynamicMavenProperty(key, projectRoot, properties); ok {
				value = &v
			}
			dynamicValues[key] = value
		}
		if value == nil {
			if v, ok := properties[key]; ok {
				value = &v
			}
		}
		if value == nil {
			unresolved = append(unresolved, key)
			return token
		}
		substituted = append(substituted, key)
		return *value
	})
	if opts.Verbose && len(substituted) > 0 {
		fmt.Printf("Maven filtering resolved: %s\n", strings.Join(uniqueStrings(substituted), ", "))
	}
	if len(unresolved) > 0 {
		fmt.Printf("WARNING: Maven filtering could not resolve: %s (left as-is)\n", strings.Join(uniqueStrings(unresolved), ", "))
	}
	return filtered, nil
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// withSourceFile calls fn with the path to sync: a temp file with Maven
// ${...} tokens substituted when the pom marks the file as a filtered
// resource, otherwise the file itself. The temp file is always deleted,
// whatever fn does. If filtering fails (e.g. the pom is unreadable mid-edit)
// fn is NOT called — keeping the last good deployed copy beats syncing a file
// with unresolved ${...} tokens that would break property resolution.
func withSourceFile(file, projectRoot string, fn func(source string)) {
	tmp, err := filteredTempFile(file, projectRoot)
	if err != nil {
		fmt.Printf("WARNING: Maven resource filtering failed for %s: %v. Skipping sync, keeping the deployed file unchanged.\n", file, err)
		return
	}
	source := file
	if tmp != "" {
		defer os.Remove(tmp)
		fmt.Printf("Applied Maven resource filtering to %s\n", filepath.Base(file))
		source = tmp
	}
	fn(source)
}

// filteredTempFile writes the Maven-filtered content of file to a temp file
// and returns its path, or "" when the file is not a filtered resource (or
// not valid UTF-8, which is synced raw). The temp file carries file's
// permissions so the deployed copy keeps them.
func filteredTempFile(file, projectRoot string) (string, error) {
	if !opts.ResourceFiltering || projectRoot == "" || !resourcesFile(file) {
		return "", nil
	}
	if filtered, err := filteredResource(file, projectRoot); err != nil || !filtered {
		return "", err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", nil
	}
	content, err := filterMavenTokens(string(data), projectRoot)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	ext := filepath.Ext(file)
	tmp, err := os.CreateTemp("", strings.TrimSuffix(filepath.Base(file), ext)+"-*"+ext)
	if err != nil {
		return "", err
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Chmod(tmp.Name(), info.Mode().Perm()); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}
