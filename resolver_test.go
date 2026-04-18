package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// mkTree creates files/dirs under root. Paths ending with "/" are dirs.
func mkTree(t *testing.T, root string, paths []string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(root, p)
		if len(p) > 0 && p[len(p)-1] == '/' {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScanClassification(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{
		"fzf/linux-amd64",
		"fzf/linux-amd64.tar.gz",
		"fzf/darwin-arm64",
		"ripgrep/14.0.3/linux-amd64.tar.gz",
		"ripgrep/14.1.0/linux-amd64.tar.gz",
		"ripgrep/14.1.0/darwin-arm64.tar.gz",
		"deploy-script/1.1.0",
		"deploy-script/1.2.0",
		"deploy-script/latest",
		"deploy-script/bundle.tar.gz",
	})

	cases := []struct {
		name    string
		wantLen int
	}{
		{"fzf", 3},
		{"ripgrep", 3},
		{"deploy-script", 4},
	}
	for _, c := range cases {
		arts, err := scan(root, c.name)
		if err != nil {
			t.Fatalf("scan %s: %v", c.name, err)
		}
		if len(arts) != c.wantLen {
			t.Errorf("scan %s: got %d artifacts, want %d: %+v", c.name, len(arts), c.wantLen, arts)
		}
	}
}

func TestScanNotFound(t *testing.T) {
	root := t.TempDir()
	_, err := scan(root, "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestScanInvalidName(t *testing.T) {
	root := t.TempDir()
	_, err := scan(root, "../etc")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestResolveRipgrep(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{
		"ripgrep/14.0.3/linux-amd64.tar.gz",
		"ripgrep/14.1.0/linux-amd64.tar.gz",
		"ripgrep/14.1.0/darwin-arm64.tar.gz",
	})
	arts, _ := scan(root, "ripgrep")

	cases := []struct {
		os, arch, ver string
		wantPath      string
		wantErr       error
	}{
		{"linux", "amd64", "", filepath.Join("ripgrep", "14.1.0", "linux-amd64.tar.gz"), nil},
		{"linux", "amd64", "14.0.3", filepath.Join("ripgrep", "14.0.3", "linux-amd64.tar.gz"), nil},
		{"linux", "arm64", "", "", ErrNotFound},
		{"", "", "", "", ErrNotFound}, // platform-agnostic query on platform-specific pkg
	}
	for _, c := range cases {
		got, err := resolve(arts, c.os, c.arch, c.ver)
		if c.wantErr != nil {
			if !errors.Is(err, c.wantErr) {
				t.Errorf("resolve(%v,%v,%v): want err %v, got %v", c.os, c.arch, c.ver, c.wantErr, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolve(%v,%v,%v): unexpected err %v", c.os, c.arch, c.ver, err)
			continue
		}
		if got.Path != c.wantPath {
			t.Errorf("resolve(%v,%v,%v): got %s, want %s", c.os, c.arch, c.ver, got.Path, c.wantPath)
		}
	}
}

func TestResolveDeployScript(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{
		"deploy-script/1.1.0",
		"deploy-script/1.2.0",
		"deploy-script/bundle.tar.gz",
	})
	arts, _ := scan(root, "deploy-script")

	// No os/arch → platform-agnostic pool; semver 1.2.0 > 1.1.0 > "bundle" (non-semver)
	got, err := resolve(arts, "", "", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Version != "1.2.0" {
		t.Errorf("want 1.2.0, got %s (path=%s)", got.Version, got.Path)
	}

	// Explicit version
	got, err = resolve(arts, "", "", "1.1.0")
	if err != nil {
		t.Fatalf("resolve ver: %v", err)
	}
	if got.Version != "1.1.0" {
		t.Errorf("want 1.1.0, got %s", got.Version)
	}

	// Platform query on platform-agnostic pkg → 404
	_, err = resolve(arts, "linux", "amd64", "")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestSemverRegression(t *testing.T) {
	// 1.10.0 > 1.9.0 > 1.2.0 (not lexical)
	root := t.TempDir()
	mkTree(t, root, []string{
		"tool/1.2.0/linux-amd64",
		"tool/1.9.0/linux-amd64",
		"tool/1.10.0/linux-amd64",
	})
	arts, _ := scan(root, "tool")
	got, err := resolve(arts, "linux", "amd64", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "1.10.0" {
		t.Errorf("want 1.10.0, got %s", got.Version)
	}
}

func TestBareFileOverArchive(t *testing.T) {
	// Same version, both bare binary and tarball → prefer bare
	root := t.TempDir()
	mkTree(t, root, []string{
		"tool/1.0.0/linux-amd64",
		"tool/1.0.0/linux-amd64.tar.gz",
	})
	arts, _ := scan(root, "tool")
	got, err := resolve(arts, "linux", "amd64", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ext != "" {
		t.Errorf("want bare file (ext=\"\"), got ext=%q path=%s", got.Ext, got.Path)
	}
}

func TestVersionlessAndVersionedMixed(t *testing.T) {
	// B + C coexist. Versioned wins because 1.0.0 > "" in our ordering.
	root := t.TempDir()
	mkTree(t, root, []string{
		"tool/linux-amd64",
		"tool/1.0.0/linux-amd64",
	})
	arts, _ := scan(root, "tool")
	got, err := resolve(arts, "linux", "amd64", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "1.0.0" {
		t.Errorf("want versioned 1.0.0 to win, got version=%q path=%s", got.Version, got.Path)
	}
}

func TestOSArchMismatch(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{"tool/linux-amd64"})
	arts, _ := scan(root, "tool")
	_, err := resolve(arts, "linux", "", "")
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("want ErrInvalid for os without arch, got %v", err)
	}
}

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.10.0", "1.9.0", 1},
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "2.0.0", -1},
		{"v1.0.0", "1.0.0", 0},
		{"1.0.0", "latest", 1}, // semver > non-semver
		{"abc", "abd", -1},
	}
	for _, c := range cases {
		got := compareVersion(c.a, c.b)
		if (got < 0) != (c.want < 0) || (got > 0) != (c.want > 0) || (got == 0) != (c.want == 0) {
			t.Errorf("compareVersion(%q,%q)=%d, want %d", c.a, c.b, got, c.want)
		}
	}
}
