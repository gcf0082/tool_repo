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

func TestScanLayout(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{
		"ripgrep/14.0.3/linux-amd64/ripgrep.tar.gz",
		"ripgrep/14.1.0/linux-amd64/ripgrep.tar.gz",
		"ripgrep/14.1.0/darwin-arm64/ripgrep.tar.gz",
		"fzf/0.1.0/linux-amd64/fzf",
	})

	arts, err := scan(root, "ripgrep")
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 3 {
		t.Errorf("scan ripgrep: got %d, want 3: %+v", len(arts), arts)
	}

	arts, err = scan(root, "fzf")
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 1 {
		t.Errorf("scan fzf: got %d, want 1", len(arts))
	}
}

func TestScanSkipsInvalidEntries(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{
		"foo/1.0.0/linux-amd64/a",
		"foo/1.0.0/bogus-platform/b", // unknown OS/arch → skipped
		"foo/stray-file",             // file at name level → skipped
		"foo/1.0.0/linux-amd64/nested/", // directory inside platform → skipped
	})
	arts, err := scan(root, "foo")
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 1 || arts[0].Platform != "linux-amd64" {
		t.Errorf("got %+v, want single linux-amd64 artifact", arts)
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

func TestResolvePicksLatestByPlatform(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{
		"ripgrep/14.0.3/linux-amd64/ripgrep.tar.gz",
		"ripgrep/14.1.0/linux-amd64/ripgrep.tar.gz",
		"ripgrep/14.1.0/darwin-arm64/ripgrep.tar.gz",
	})
	arts, _ := scan(root, "ripgrep")

	cases := []struct {
		os, arch, ver string
		wantPath      string
		wantErr       error
	}{
		{"linux", "amd64", "", filepath.Join("ripgrep", "14.1.0", "linux-amd64", "ripgrep.tar.gz"), nil},
		{"linux", "amd64", "14.0.3", filepath.Join("ripgrep", "14.0.3", "linux-amd64", "ripgrep.tar.gz"), nil},
		{"darwin", "arm64", "", filepath.Join("ripgrep", "14.1.0", "darwin-arm64", "ripgrep.tar.gz"), nil},
		{"linux", "arm64", "", "", ErrNotFound},
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

func TestResolveRequiresOSArch(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{"x/1.0.0/linux-amd64/a"})
	arts, _ := scan(root, "x")

	for _, c := range []struct{ os, arch string }{
		{"", ""}, {"linux", ""}, {"", "amd64"},
	} {
		_, err := resolve(arts, c.os, c.arch, "")
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("resolve(%q,%q): want ErrInvalid, got %v", c.os, c.arch, err)
		}
	}
}

func TestSemverRegression(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, []string{
		"tool/1.2.0/linux-amd64/tool",
		"tool/1.9.0/linux-amd64/tool",
		"tool/1.10.0/linux-amd64/tool",
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
	// Same version and platform, both bare and tarball → prefer bare.
	root := t.TempDir()
	mkTree(t, root, []string{
		"tool/1.0.0/linux-amd64/tool",
		"tool/1.0.0/linux-amd64/tool.tar.gz",
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

func TestAmbiguousSameExt(t *testing.T) {
	// Two bare files same version + platform → ambiguous.
	root := t.TempDir()
	mkTree(t, root, []string{
		"tool/1.0.0/linux-amd64/tool",
		"tool/1.0.0/linux-amd64/tool-alt",
	})
	arts, _ := scan(root, "tool")
	_, err := resolve(arts, "linux", "amd64", "")
	if !errors.Is(err, ErrAmbiguous) {
		t.Errorf("want ErrAmbiguous, got %v", err)
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
		{"1.0.0", "latest", 1},
		{"abc", "abd", -1},
	}
	for _, c := range cases {
		got := compareVersion(c.a, c.b)
		if (got < 0) != (c.want < 0) || (got > 0) != (c.want > 0) || (got == 0) != (c.want == 0) {
			t.Errorf("compareVersion(%q,%q)=%d, want %d", c.a, c.b, got, c.want)
		}
	}
}
