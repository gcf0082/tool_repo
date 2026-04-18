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

func isPlatform(stem string) (platform string, ok bool) {
	m := platformRe.FindStringSubmatch(stem)
	if m == nil {
		return "", false
	}
	if !allowedOS[m[1]] || !allowedArch[m[2]] {
		return "", false
	}
	return stem, true
}

func scan(root, name string) ([]Artifact, error) {
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("%w: bad name %q", ErrInvalid, name)
	}
	dir := filepath.Join(root, name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	var arts []Artifact
	for _, e := range entries {
		if e.IsDir() {
			// Layout C: version directory
			ver := e.Name()
			if !nameRe.MatchString(ver) {
				continue
			}
			subs, err := os.ReadDir(filepath.Join(dir, ver))
			if err != nil {
				continue
			}
			for _, s := range subs {
				if s.IsDir() {
					continue
				}
				stem, ext := splitArchiveExt(s.Name())
				plat, ok := isPlatform(stem)
				if !ok {
					continue
				}
				arts = append(arts, Artifact{
					Path:     filepath.Join(name, ver, s.Name()),
					Name:     name,
					Version:  ver,
					Platform: plat,
					Ext:      ext,
				})
			}
			continue
		}
		// File at packages/<name>/
		stem, ext := splitArchiveExt(e.Name())
		if plat, ok := isPlatform(stem); ok {
			// Layout B: versionless platform-specific
			arts = append(arts, Artifact{
				Path:     filepath.Join(name, e.Name()),
				Name:     name,
				Version:  "",
				Platform: plat,
				Ext:      ext,
			})
			continue
		}
		// Layout D: platform-agnostic, file name (with ext) is identifier
		arts = append(arts, Artifact{
			Path:     filepath.Join(name, e.Name()),
			Name:     name,
			Version:  stem,
			Platform: "",
			Ext:      ext,
		})
	}
	return arts, nil
}

// compareVersion returns -1/0/1 for a<b/a==b/a>b. Prefers semver; falls back
// to lexical. Semver beats non-semver (semver is considered "larger").
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

// resolve filters artifacts by query and picks the best single candidate.
func resolve(arts []Artifact, osName, arch, version string) (*Artifact, error) {
	if (osName == "") != (arch == "") {
		return nil, fmt.Errorf("%w: os and arch must be given together", ErrInvalid)
	}
	wantPlat := ""
	if osName != "" {
		wantPlat = osName + "-" + arch
	}

	var cands []Artifact
	for _, a := range arts {
		if wantPlat == "" {
			if a.Platform != "" {
				continue
			}
		} else {
			if a.Platform != wantPlat {
				continue
			}
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
		// Same version: prefer bare file over archives
		return extRank(cands[i].Ext) < extRank(cands[j].Ext)
	})

	best := cands[0]
	// Detect ambiguity: multiple with identical (version, ext) score
	if len(cands) > 1 {
		second := cands[1]
		if compareVersion(best.Version, second.Version) == 0 && extRank(best.Ext) == extRank(second.Ext) {
			return nil, fmt.Errorf("%w: %s vs %s", ErrAmbiguous, best.Path, second.Path)
		}
	}
	return &best, nil
}

// extRank: smaller = more preferred.
func extRank(ext string) int {
	switch ext {
	case "":
		return 0
	case ".tar.gz", ".tgz":
		return 1
	case ".zip":
		return 2
	default:
		return 3
	}
}
