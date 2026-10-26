// Command third-party-licenses collects the license terms of everything linked
// into a binary, for shipping next to it.
//
//	go run ./scripts/third-party-licenses -out dist/third_party -goos linux -goarch amd64
//
// It asks `go list -deps` which packages the binary links on the target platform
// (-goos/-goarch, default the host's; CGO off, as the release builds) and for every
// module they come from it copies the module's LICENSE / COPYING / NOTICE /
// PATENTS files. For modules under a file-level copyleft licence (MPL-2.0, LGPL)
// it also copies the source files of the linked packages, which is what those
// licences ask for when a binary is distributed; the Go standard library's terms
// are included as well, since it is linked statically.
//
// The run fails when a module has no license file, or when its terms cannot be
// recognised or are not ones a statically linked Apache-2.0 binary may carry, so
// an unreviewed dependency cannot reach a release unnoticed. LICENSE and NOTICE in
// the working directory, when present, are copied to the top of the output: they
// are the terms of SAiCoN itself.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type module struct {
	Path    string
	Version string
	Dir     string
	Main    bool
	Replace *module
}

type pkg struct {
	ImportPath string
	Dir        string
	Standard   bool
	Module     *module
	GoFiles    []string
	CgoFiles   []string
	SFiles     []string
	HFiles     []string
	CFiles     []string
	EmbedFiles []string
}

type dep struct {
	path, version, dir string
	pkgs               []pkg
	files              []string
	license            string
}

// goos and goarch name the release target. The tool itself runs on the host, so
// they are passed to `go list` only, never to the environment of `go run`.
var goos, goarch string

// sourceURL and sourceRef tell a recipient where the complete corresponding source
// is, which the LGPL and the MPL ask the distributor to say.
var sourceURL, sourceRef string

func targetEnv() []string {
	env := append(os.Environ(), "CGO_ENABLED=0")
	if goos != "" {
		env = append(env, "GOOS="+goos)
	}
	if goarch != "" {
		env = append(env, "GOARCH="+goarch)
	}
	return env
}

var licenseFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|patents)([._-].*)?$`)

func main() {
	out := flag.String("out", "", "output directory (created; must not exist or be empty)")
	target := flag.String("pkg", "./cmd/saicond", "package whose binary is described")
	flag.StringVar(&goos, "goos", "", "target GOOS (default: the host's)")
	flag.StringVar(&goarch, "goarch", "", "target GOARCH (default: the host's)")
	flag.StringVar(&sourceURL, "source", "https://github.com/saicon-network/saicon", "where the complete SAiCoN source is published")
	flag.StringVar(&sourceRef, "ref", "", "the release tag or commit the binary is built from")
	flag.Parse()
	if *out == "" {
		fail(errors.New("-out is required"))
	}
	if err := run(*out, *target); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "third-party-licenses:", err)
	os.Exit(1)
}

func run(out, target string) error {
	if entries, err := os.ReadDir(out); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s is not empty", out)
	}
	pkgs, err := listDeps(target)
	if err != nil {
		return err
	}

	deps := map[string]*dep{}
	for _, p := range pkgs {
		if p.Standard || p.Module == nil || p.Module.Main {
			continue
		}
		m := p.Module
		// A replaced module is labelled by what is actually linked: the replacement's
		// path and version (or "local" for a directory replace), with the original
		// path kept for the reader. Module.Dir already points at the replacement.
		label, path, version, dir := m.Path, m.Path, m.Version, m.Dir
		if r := m.Replace; r != nil {
			path, version = r.Path, r.Version
			if version == "" {
				version = "local"
			}
			if r.Dir != "" {
				dir = r.Dir
			}
			label = m.Path + " => " + r.Path
		}
		key := path + "@" + version
		d := deps[key]
		if d == nil {
			d = &dep{path: label, version: version, dir: dir}
			deps[key] = d
		}
		d.pkgs = append(d.pkgs, p)
	}

	var problems []string
	for _, d := range deps {
		files, text, err := licenseFiles(d)
		if err != nil {
			return err
		}
		d.files = files
		if !hasRootLicense(files) {
			problems = append(problems, fmt.Sprintf("%s@%s: no license file in %s", d.path, d.version, d.dir))
			continue
		}
		d.license = classify(text, d)
		if d.license == "" {
			problems = append(problems, fmt.Sprintf("%s@%s: license not recognised (%s)", d.path, d.version, strings.Join(files, ", ")))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("dependencies that need review:\n  %s", strings.Join(problems, "\n  "))
	}

	keys := make([]string, 0, len(deps))
	for k := range deps {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		d := deps[k]
		dst := filepath.Join(out, "licenses", filepath.FromSlash(k))
		for _, f := range d.files {
			if err := copyFile(filepath.Join(d.dir, filepath.FromSlash(f)), filepath.Join(dst, filepath.FromSlash(f))); err != nil {
				return err
			}
		}
		if reciprocal(d.license) {
			for _, p := range d.pkgs {
				rel, err := filepath.Rel(d.dir, p.Dir)
				if err != nil {
					return err
				}
				for _, f := range sourceFiles(p) {
					src := filepath.Join(p.Dir, f)
					if err := copyFile(src, filepath.Join(out, "source", filepath.FromSlash(k), rel, f)); err != nil {
						return err
					}
				}
			}
		}
	}

	goVersion, goroot, err := goEnv()
	if err != nil {
		return err
	}
	for _, f := range []string{"LICENSE", "PATENTS"} {
		src := filepath.Join(goroot, f)
		if _, err := os.Stat(src); err == nil {
			if err := copyFile(src, filepath.Join(out, "licenses", "go@"+goVersion, f)); err != nil {
				return err
			}
		}
	}
	for _, f := range []string{"LICENSE", "NOTICE"} {
		if _, err := os.Stat(f); err == nil {
			if err := copyFile(f, filepath.Join(out, f)); err != nil {
				return err
			}
		}
	}

	return writeIndex(out, target, goVersion, keys, deps)
}

func listDeps(target string) ([]pkg, error) {
	cmd := exec.Command("go", "list", "-deps", "-json", target)
	cmd.Env = targetEnv()
	cmd.Stderr = os.Stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	var pkgs []pkg
	dec := json.NewDecoder(bytes.NewReader(raw))
	for {
		var p pkg
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("go list output: %w", err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

func goEnv() (version, goroot string, err error) {
	raw, err := exec.Command("go", "env", "GOVERSION", "GOROOT").Output()
	if err != nil {
		return "", "", fmt.Errorf("go env: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		return "", "", fmt.Errorf("go env: unexpected output %q", raw)
	}
	return strings.TrimPrefix(lines[0], "go"), lines[1], nil
}

// licenseFiles returns the license files that govern the linked packages of a
// module, as slash paths relative to its root, and their combined text. Besides
// the root it looks in every directory from a linked package up to the root: code
// vendored into a module often keeps its own licence beside it
// (klauspost/compress/zstd/internal/xxhash, go-kit/log/term), and that notice must
// travel too — and must be classified, so a copyleft subdirectory cannot hide
// inside a permissive module.
func licenseFiles(d *dep) ([]string, string, error) {
	dirs := map[string]bool{d.dir: true}
	for _, p := range d.pkgs {
		for dir := p.Dir; ; dir = filepath.Dir(dir) {
			rel, err := filepath.Rel(d.dir, dir)
			if err != nil || strings.HasPrefix(rel, "..") {
				break
			}
			dirs[dir] = true
			if rel == "." {
				break
			}
		}
	}
	var files []string
	var text strings.Builder
	for dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, "", err
		}
		for _, e := range entries {
			if e.IsDir() || !licenseFile.MatchString(e.Name()) {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return nil, "", err
			}
			rel, _ := filepath.Rel(d.dir, filepath.Join(dir, e.Name()))
			files = append(files, filepath.ToSlash(rel))
			text.Write(b)
			text.WriteString("\n")
		}
	}
	sort.Strings(files)
	return files, text.String(), nil
}

func hasRootLicense(files []string) bool {
	for _, f := range files {
		if !strings.Contains(f, "/") {
			return true
		}
	}
	return false
}

// classify names the terms a module ships under, from its license files. Every
// licence found is listed: some modules carry several (MIT and Apache-2.0 for code
// of different origin, or one licence per platform). A module carrying both the GPL
// and the LGPL texts (the LGPL is written as additional permissions on top of the
// GPL) is LGPL, and that is accepted only after checking that every linked source
// file says so in its header. An empty result means the terms need a person.
func classify(text string, d *dep) string {
	t := strings.Join(strings.Fields(text), " ")
	// Terms that restrict use or sale are refused outright, even when they name a
	// permissive licence (a Business Source License's Change License, Apache-2.0
	// with the Commons Clause): matching only the permissive name would accept them.
	lower := strings.ToLower(t)
	for _, marker := range restrictive {
		if strings.Contains(lower, marker) {
			return ""
		}
	}
	if strings.Contains(t, "GNU LESSER GENERAL PUBLIC LICENSE") {
		for _, p := range d.pkgs {
			for _, f := range append(append(append([]string{}, p.GoFiles...), p.CgoFiles...), p.SFiles...) {
				head, _ := readHead(filepath.Join(p.Dir, f))
				if !strings.Contains(head, "GNU Lesser General Public License") {
					return ""
				}
			}
		}
		return "LGPL-3.0-or-later"
	}
	if strings.Contains(t, "GNU GENERAL PUBLIC LICENSE") || strings.Contains(t, "GNU AFFERO") {
		return ""
	}
	var found []string
	add := func(ok bool, name string) {
		if ok {
			found = append(found, name)
		}
	}
	add(strings.Contains(t, "Apache License") && strings.Contains(t, "Version 2.0"), "Apache-2.0")
	if strings.Contains(t, "Redistribution and use in source and binary forms") {
		// The third BSD clause forbids using the authors' names to endorse derived
		// products; its wording varies, its verb does not.
		if strings.Contains(t, "to endorse or promote") {
			add(true, "BSD-3-Clause")
		} else {
			add(true, "BSD-2-Clause")
		}
	}
	add(strings.Contains(t, "Permission to use, copy, modify, and/or distribute this software") ||
		strings.Contains(t, "Permission to use, copy, modify, and distribute this software for any purpose with or without fee"), "ISC")
	add(strings.Contains(t, "Permission is hereby granted, free of charge"), "MIT")
	add(strings.Contains(t, "Mozilla Public License Version 2.0") || strings.Contains(t, "Mozilla Public License, version 2.0"), "MPL-2.0")
	add(strings.Contains(t, "This is free and unencumbered software released into the public domain"), "Unlicense")
	return strings.Join(found, " AND ")
}

// Each marker is a phrase only a restrictive licence uses. A bare "noncommercial"
// would not do: the GPLv3 itself allows conveying "occasionally and noncommercially".
var restrictive = []string{
	"business source license", "change license:", "commons clause",
	"server side public license", "elastic license", "polyform",
	"attribution-noncommercial", "noncommercial use only", "non-commercial use only",
	"for noncommercial purposes", "for non-commercial purposes",
	"confluent community license",
}

func reciprocal(license string) bool {
	return strings.Contains(license, "MPL-2.0") || strings.HasPrefix(license, "LGPL")
}

func readHead(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 2048)
	n, _ := io.ReadFull(f, buf)
	return string(buf[:n]), nil
}

func sourceFiles(p pkg) []string {
	var all []string
	for _, group := range [][]string{p.GoFiles, p.CgoFiles, p.SFiles, p.HFiles, p.CFiles, p.EmbedFiles} {
		all = append(all, group...)
	}
	sort.Strings(all)
	return all
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	outF, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(outF, in); err != nil {
		_ = outF.Close()
		return err
	}
	return outF.Close()
}

func writeIndex(out, target, goVersion string, keys []string, deps map[string]*dep) error {
	var b strings.Builder
	cmd := exec.Command("go", "env", "GOOS", "GOARCH")
	cmd.Env = targetEnv()
	raw, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("go env: %w", err)
	}
	f := strings.Fields(string(raw))
	if len(f) != 2 {
		return fmt.Errorf("go env: unexpected output %q", raw)
	}
	goos, goarch := f[0], f[1]
	fmt.Fprintf(&b, "# Third-party licenses\n\n")
	fmt.Fprintf(&b, "The `%s` binary for %s/%s links the Go standard library (Go %s) and the modules\n", filepath.Base(target), goos, goarch, goVersion)
	fmt.Fprintf(&b, "below. Each module's license files are in `licenses/<module>@<version>/`.\n\n")
	fmt.Fprintf(&b, "Modules under MPL-2.0 or the LGPL are linked unmodified. The source of every package of\n")
	fmt.Fprintf(&b, "theirs that the binary links is in `source/<module>@<version>/`. The complete source of\n")
	if sourceRef != "" {
		fmt.Fprintf(&b, "SAiCoN is at %s, tag `%s`,\n", sourceURL, sourceRef)
	} else {
		fmt.Fprintf(&b, "SAiCoN is at %s,\n", sourceURL)
	}
	fmt.Fprintf(&b, "so the binary can be rebuilt against a modified copy of any of them\n")
	fmt.Fprintf(&b, "(`go mod edit -replace`, then `go build ./cmd/saicond`).\n\n")
	fmt.Fprintf(&b, "| Module | Version | License | Files |\n|---|---|---|---|\n")
	counts := map[string]int{}
	for _, k := range keys {
		d := deps[k]
		counts[d.license]++
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", d.path, d.version, d.license, strings.Join(d.files, ", "))
	}
	fmt.Fprintf(&b, "| Go standard library | %s | BSD-3-Clause | LICENSE, PATENTS |\n", goVersion)
	names := make([]string, 0, len(counts))
	for l := range counts {
		names = append(names, l)
	}
	sort.Strings(names)
	fmt.Fprintf(&b, "\n%d modules:", len(keys))
	for _, l := range names {
		fmt.Fprintf(&b, " %s %d", l, counts[l])
	}
	fmt.Fprintf(&b, ".\n")
	return os.WriteFile(filepath.Join(out, "THIRD_PARTY_LICENSES.md"), []byte(b.String()), 0o644)
}
