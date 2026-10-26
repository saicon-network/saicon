package main

import (
	"os"
	"path/filepath"
	"testing"
)

const bsdHead = "Redistribution and use in source and binary forms, with or without modification, are permitted provided that the following conditions are met: "

func TestClassify(t *testing.T) {
	cases := []struct {
		name, text, want string
	}{
		{"apache", "Apache License\n Version 2.0, January 2004", "Apache-2.0"},
		{"mit", "Permission is hereby granted, free of charge, to any person", "MIT"},
		{"bsd-2", bsdHead + "1. ... 2. ...", "BSD-2-Clause"},
		{"bsd-3, the usual wording", bsdHead + "Neither the name of Google Inc. nor the names of its contributors may be used to endorse or promote products", "BSD-3-Clause"},
		{"bsd-3, the author named", bsdHead + "* My name, Ulrich Kunitz, may not be used to endorse or promote products", "BSD-3-Clause"},
		{"isc", "Permission to use, copy, modify, and/or distribute this software for any purpose", "ISC"},
		{"mpl", "Mozilla Public License Version 2.0\n==================", "MPL-2.0"},
		{"two licenses", "Permission is hereby granted, free of charge\n\nApache License Version 2.0", "Apache-2.0 AND MIT"},
		{"gpl is refused", "GNU GENERAL PUBLIC LICENSE Version 3", ""},
		{"agpl is refused", "GNU AFFERO GENERAL PUBLIC LICENSE", ""},
		{"unknown is refused", "All rights reserved.", ""},
		{"BUSL naming an MPL change license is refused", "Business Source License 1.1 ... Change License: Mozilla Public License, version 2.0", ""},
		{"Apache with the Commons Clause is refused", "Apache License Version 2.0 ... \"Commons Clause\" License Condition v1.0", ""},
		{"PolyForm Noncommercial is refused", "PolyForm Noncommercial License 1.0.0 ... Permission is hereby granted, free of charge", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.text, &dep{}); got != tc.want {
				t.Fatalf("classify = %q, want %q", got, tc.want)
			}
		})
	}
}

// A module shipping the LGPL (with the GPL it amends) is accepted only when every
// linked file declares the LGPL; one file under the plain GPL refuses the module.
func TestClassifyLGPLChecksEveryLinkedFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, header string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(header+"\npackage x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "// under the terms of the GNU Lesser General Public License as published by")
	write("b.go", "// under the terms of the GNU Lesser General Public License as published by")
	text := "GNU GENERAL PUBLIC LICENSE\n...\nGNU LESSER GENERAL PUBLIC LICENSE"

	d := &dep{pkgs: []pkg{{Dir: dir, GoFiles: []string{"a.go", "b.go"}}}}
	if got := classify(text, d); got != "LGPL-3.0-or-later" {
		t.Fatalf("all files LGPL: classify = %q", got)
	}

	write("c.go", "// under the terms of the GNU General Public License as published by")
	d.pkgs[0].GoFiles = append(d.pkgs[0].GoFiles, "c.go")
	if got := classify(text, d); got != "" {
		t.Fatalf("one file GPL: classify = %q, want refusal", got)
	}
}

func TestReciprocal(t *testing.T) {
	for lic, want := range map[string]bool{
		"MPL-2.0": true, "LGPL-3.0-or-later": true, "Apache-2.0 AND MPL-2.0": true,
		"Apache-2.0": false, "MIT": false, "BSD-3-Clause": false,
	} {
		if got := reciprocal(lic); got != want {
			t.Errorf("reciprocal(%q) = %v, want %v", lic, got, want)
		}
	}
}

// A licence kept beside vendored code below the module root is collected and
// classified with the module: here an MIT notice under a BSD module, and a GPL one
// that must refuse it.
func TestLicenseFilesIncludesSubdirectories(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "internal", "xxhash")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	put := func(path, text string) {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(filepath.Join(root, "LICENSE"), bsdHead+"may not be used to endorse or promote")
	put(filepath.Join(sub, "LICENSE.txt"), "Permission is hereby granted, free of charge")
	put(filepath.Join(root, "internal", "README"), "not a license")

	d := &dep{dir: root, pkgs: []pkg{{Dir: sub}}}
	files, text, err := licenseFiles(d)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"LICENSE", "internal/xxhash/LICENSE.txt"}; !equal(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
	if got := classify(text, d); got != "BSD-3-Clause AND MIT" {
		t.Fatalf("classify = %q", got)
	}

	put(filepath.Join(sub, "LICENSE.txt"), "GNU GENERAL PUBLIC LICENSE Version 3")
	_, text, _ = licenseFiles(d)
	if got := classify(text, d); got != "" {
		t.Fatalf("a GPL subdirectory must refuse the module, got %q", got)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
