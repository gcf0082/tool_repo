package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
)

// Accepted package file formats. The file name itself is free-form;
// it just has to end with one of these extensions.
var archiveExts = []string{
	".tar.gz", ".tgz",
	".tar.bz2", ".tbz2",
	".tar.xz", ".txz",
	".zip", ".7z",
}

var (
	allowedOS   = map[string]bool{"linux": true, "darwin": true, "windows": true}
	allowedArch = map[string]bool{"amd64": true, "arm64": true, "386": true, "arm": true}

	nameRe     = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	platformRe = regexp.MustCompile(`^([a-z0-9]+)-([a-z0-9]+)$`)
)

// Directory layout (single, canonical):
//
//   packages/<name>/<version>/<os-arch>/<file>
//
// Every level except the leaf must be a directory. Leaf file name is
// free-form; its extension (tar.gz / bare / ...) only affects ranking
// when multiple candidates coexist for the same version+platform.
type Artifact struct {
	Path     string
	Name     string
	Version  string
	Platform string
	Ext      string
}

var (
	ErrNotFound  = errors.New("not found")
	ErrAmbiguous = errors.New("ambiguous: multiple candidates")
	ErrInvalid   = errors.New("invalid request")
)

func splitArchiveExt(fname string) (stem, ext string) {
	for _, e := range archiveExts {
		if strings.HasSuffix(fname, e) {
			return strings.TrimSuffix(fname, e), e
		}
	}
	return fname, ""
}

func isValidPlatform(plat string) bool {
	m := platformRe.FindStringSubmatch(plat)
	if m == nil {
		return false
	}
	return allowedOS[m[1]] && allowedArch[m[2]]
}

func scan(root, name string) ([]Artifact, error) {
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("%w: bad name %q", ErrInvalid, name)
	}
	dir := filepath.Join(root, name)
	versions, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	var arts []Artifact
	for _, v := range versions {
		if !v.IsDir() {
			continue
		}
		ver := v.Name()
		if !nameRe.MatchString(ver) {
			continue
		}
		platDir := filepath.Join(dir, ver)
		platforms, err := os.ReadDir(platDir)
		if err != nil {
			continue
		}
		for _, p := range platforms {
			if !p.IsDir() {
				continue
			}
			plat := p.Name()
			if !isValidPlatform(plat) {
				continue
			}
			files, err := os.ReadDir(filepath.Join(platDir, plat))
			if err != nil {
				continue
			}
			for _, f := range files {
				if f.IsDir() {
					continue
				}
				_, ext := splitArchiveExt(f.Name())
				if ext == "" {
					continue // only .tar.gz / .zip are treated as packages
				}
				arts = append(arts, Artifact{
					Path:     filepath.Join(name, ver, plat, f.Name()),
					Name:     name,
					Version:  ver,
					Platform: plat,
					Ext:      ext,
				})
			}
		}
	}
	return arts, nil
}

// compareVersion returns -1/0/1 for a<b/a==b/a>b. Prefers semver; falls
// back to lexical. Semver beats non-semver (semver is considered
// "larger") so canonical 1.2.0 wins against ad-hoc "latest" siblings.
func compareVersion(a, b string) int {
	sa := toSemverForm(a)
	sb := toSemverForm(b)
	aOK := semver.IsValid(sa)
	bOK := semver.IsValid(sb)
	switch {
	case aOK && bOK:
		return semver.Compare(sa, sb)
	case aOK && !bOK:
		return 1
	case !aOK && bOK:
		return -1
	default:
		return strings.Compare(a, b)
	}
}

func toSemverForm(v string) string {
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// resolve filters by required (os, arch) and optional version, then
// picks the best candidate.
func resolve(arts []Artifact, osName, arch, version string) (*Artifact, error) {
	if osName == "" || arch == "" {
		return nil, fmt.Errorf("%w: os and arch are required", ErrInvalid)
	}
	wantPlat := osName + "-" + arch

	var cands []Artifact
	for _, a := range arts {
		if a.Platform != wantPlat {
			continue
		}
		if version != "" && a.Version != version {
			continue
		}
		cands = append(cands, a)
	}
	if len(cands) == 0 {
		return nil, ErrNotFound
	}

	sort.SliceStable(cands, func(i, j int) bool {
		vi, vj := cands[i].Version, cands[j].Version
		if c := compareVersion(vi, vj); c != 0 {
			return c > 0 // larger version first
		}
		return extRank(cands[i].Ext) < extRank(cands[j].Ext)
	})

	best := cands[0]
	if len(cands) > 1 {
		second := cands[1]
		if compareVersion(best.Version, second.Version) == 0 && extRank(best.Ext) == extRank(second.Ext) {
			return nil, fmt.Errorf("%w: %s vs %s", ErrAmbiguous, best.Path, second.Path)
		}
	}
	return &best, nil
}

// extRank: smaller = more preferred when multiple files coexist for
// the same version+platform. Bare files aren't scanned anymore, so
// .tar.gz family leads.
func extRank(ext string) int {
	switch ext {
	case ".tar.gz", ".tgz":
		return 0
	case ".zip":
		return 1
	case ".tar.bz2", ".tbz2":
		return 2
	case ".tar.xz", ".txz":
		return 3
	case ".7z":
		return 4
	default:
		return 5
	}
}
