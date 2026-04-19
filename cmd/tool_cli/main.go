// tool_cli is a pure-Go client for tool_repo. No curl, no python3.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	httpTimeout = 30 * time.Second
	defaultDest = "/usr/local/bin"
)

type config struct {
	URL string `json:"url"`
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tool_cli", "config.json")
}

func readConfig() (config, error) {
	var c config
	f, err := os.Open(configPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
		return c, err
	}
	defer f.Close()
	_ = json.NewDecoder(f).Decode(&c)
	return c, nil
}

func writeConfig(c config) error {
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, b, 0o644)
}

func currentURL() (string, error) {
	if u := os.Getenv("TOOL_CLI_URL"); u != "" {
		return u, nil
	}
	c, err := readConfig()
	if err != nil {
		return "", err
	}
	if c.URL == "" {
		return "", fmt.Errorf("no server URL configured; run: tool_cli set-url <url>")
	}
	return c.URL, nil
}

func detectOS() (string, error) {
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
		return runtime.GOOS, nil
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func detectArch() (string, error) {
	switch runtime.GOARCH {
	case "amd64", "arm64", "386", "arm":
		return runtime.GOARCH, nil
	default:
		return "", fmt.Errorf("unsupported arch: %s", runtime.GOARCH)
	}
}

var httpClient = &http.Client{Timeout: httpTimeout}

// fetchErr typed so callers can inspect the HTTP code and customize
// the error prefix without string matching.
type fetchErr struct {
	code int
	err  error
}

func (e *fetchErr) Error() string { return e.err.Error() }
func (e *fetchErr) Code() int     { return e.code }

// friendlyFetch performs a HEAD probe and returns a helpful error
// instead of a raw 4xx. Returns (body, filename, err). body is streaming.
func friendlyFetch(rawURL string) (io.ReadCloser, string, error) {
	headReq, _ := http.NewRequest("HEAD", rawURL, nil)
	headResp, err := httpClient.Do(headReq)
	if err != nil {
		return nil, "", &fetchErr{code: 0, err: fmt.Errorf("cannot reach server: %w", err)}
	}
	headResp.Body.Close()
	switch headResp.StatusCode {
	case 200:
	case 404:
		return nil, "", &fetchErr{code: 404, err: fmt.Errorf("not found")}
	case 400:
		return nil, "", &fetchErr{code: 400, err: fmt.Errorf("bad request")}
	default:
		return nil, "", &fetchErr{code: headResp.StatusCode, err: fmt.Errorf("server returned HTTP %d", headResp.StatusCode)}
	}

	fname := filenameFromDisposition(headResp.Header.Get("Content-Disposition"))

	resp, err := httpClient.Get(rawURL)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, "", fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}
	if fname == "" {
		fname = filenameFromDisposition(resp.Header.Get("Content-Disposition"))
	}
	return resp.Body, fname, nil
}

var filenameRe = regexp.MustCompile(`filename="([^"]+)"`)

func filenameFromDisposition(cd string) string {
	if cd == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(cd)
	if err == nil {
		if f := params["filename"]; f != "" {
			return f
		}
	}
	if m := filenameRe.FindStringSubmatch(cd); m != nil {
		return m[1]
	}
	return ""
}

// ---------- commands ----------

func cmdSetURL(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: tool_cli set-url <url>")
	}
	if err := writeConfig(config{URL: args[0]}); err != nil {
		return err
	}
	fmt.Printf("saved url=%s to %s\n", args[0], configPath())
	return nil
}

func cmdConfig(args []string) error {
	p := configPath()
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Printf("(no config at %s)\n", p)
			return nil
		}
		return err
	}
	os.Stdout.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

func cmdURL(args []string) error {
	u, err := currentURL()
	if err != nil {
		fmt.Fprintln(os.Stderr, "(unset)")
		return err
	}
	fmt.Println(u)
	return nil
}

func cmdPing(args []string) error {
	base, err := currentURL()
	if err != nil {
		return err
	}
	resp, err := httpClient.Get(base + "/")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL  %s  (%v)\n", base, err)
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 200 && strings.Contains(string(body), "tool_repo") {
		fmt.Printf("OK    %s  (HTTP %d)\n", base, resp.StatusCode)
		return nil
	}
	fmt.Fprintf(os.Stderr, "FAIL  %s  (HTTP %d)\n", base, resp.StatusCode)
	return fmt.Errorf("ping failed")
}

func cmdHelp(args []string) error {
	base, err := currentURL()
	if err == nil {
		if resp, herr := httpClient.Get(base + "/tool_cli_help"); herr == nil {
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				_, _ = io.Copy(os.Stdout, resp.Body)
				return nil
			}
		}
	}
	fmt.Fprintln(os.Stderr, `tool_cli — client for tool_repo

(Help is served at $URL/tool_cli_help but that endpoint is unreachable.)
Configure the server URL first: tool_cli set-url <url>
Then retry:                     tool_cli help

Minimal command list:
  set-url, config, url, ping, get, install, run, push, help`)
	return fmt.Errorf("help: server unreachable")
}

type getFlags struct {
	name, version, osName, arch, dir string
}

func parseGetFlags(args []string) (*getFlags, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("usage: tool_cli get <name> [--version V] [--os X --arch Y] [--dir D]")
	}
	g := &getFlags{name: args[0]}
	args = args[1:]
	for len(args) > 0 {
		a := args[0]
		switch a {
		case "--version", "-v":
			if len(args) < 2 {
				return nil, fmt.Errorf("flag %s needs a value", a)
			}
			g.version = args[1]
			args = args[2:]
		case "--os":
			if len(args) < 2 {
				return nil, fmt.Errorf("flag %s needs a value", a)
			}
			g.osName = args[1]
			args = args[2:]
		case "--arch":
			if len(args) < 2 {
				return nil, fmt.Errorf("flag %s needs a value", a)
			}
			g.arch = args[1]
			args = args[2:]
		case "--dir", "-d":
			if len(args) < 2 {
				return nil, fmt.Errorf("flag %s needs a value", a)
			}
			g.dir = args[1]
			args = args[2:]
		default:
			return nil, fmt.Errorf("unknown arg: %s", a)
		}
	}
	if g.osName == "" {
		s, err := detectOS()
		if err != nil {
			return nil, err
		}
		g.osName = s
	}
	if g.arch == "" {
		a, err := detectArch()
		if err != nil {
			return nil, err
		}
		g.arch = a
	}
	return g, nil
}

func cmdGet(args []string) error {
	g, err := parseGetFlags(args)
	if err != nil {
		return err
	}
	base, err := currentURL()
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/get_tool?name=%s&os=%s&arch=%s",
		base, url.QueryEscape(g.name), url.QueryEscape(g.osName), url.QueryEscape(g.arch))
	if g.version != "" {
		u += "&version=" + url.QueryEscape(g.version)
	}
	fmt.Fprintf(os.Stderr, "GET %s\n", u)
	body, fname, err := friendlyFetch(u)
	if err != nil {
		return err
	}
	defer body.Close()
	if fname == "" {
		fname = g.name
	}
	dest := g.dir
	if dest == "" {
		dest = "."
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	out := filepath.Join(dest, fname)
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, body); err != nil {
		f.Close()
		return err
	}
	f.Close()
	fmt.Printf("saved %s\n", out)
	return nil
}

// installExecutableMode adds +x to the given mode so extracted files
// in install target are runnable. Tool archives routinely ship with
// 0644 mode for convenience when authors didn't chmod before taring.
func installExecutableMode(m os.FileMode) os.FileMode {
	return m | 0o111
}

func cmdInstall(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: tool_cli install <name> [--dir D]")
	}
	g := &getFlags{name: args[0], dir: defaultDest}
	args = args[1:]
	for len(args) > 0 {
		switch args[0] {
		case "--dir", "-d":
			if len(args) < 2 {
				return fmt.Errorf("flag %s needs a value", args[0])
			}
			g.dir = args[1]
			args = args[2:]
		case "--version", "-v":
			if len(args) < 2 {
				return fmt.Errorf("flag %s needs a value", args[0])
			}
			g.version = args[1]
			args = args[2:]
		default:
			return fmt.Errorf("unknown arg: %s", args[0])
		}
	}
	if g.osName == "" {
		g.osName, _ = detectOS()
	}
	if g.arch == "" {
		g.arch, _ = detectArch()
	}
	base, err := currentURL()
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/get_tool?name=%s&os=%s&arch=%s",
		base, url.QueryEscape(g.name), url.QueryEscape(g.osName), url.QueryEscape(g.arch))
	if g.version != "" {
		u += "&version=" + url.QueryEscape(g.version)
	}
	body, fname, err := friendlyFetch(u)
	if err != nil {
		return err
	}
	defer body.Close()
	if err := os.MkdirAll(g.dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", g.dir, err)
	}
	n, err := extractArchive(body, fname, g.dir, true /* chmod +x */)
	if err != nil {
		return err
	}
	fmt.Printf("installed %d file(s) into %s\n", n, g.dir)
	return nil
}

// extractArchive writes the contents of the named archive stream into
// destDir. When chmodX is true, regular files get the exec bit set so
// installed tools are immediately runnable. Supports .tar.gz/.tgz and
// .zip; other formats are saved verbatim to destDir/<filename>.
func extractArchive(r io.Reader, filename, destDir string, chmodX bool) (int, error) {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarGz(r, destDir, chmodX)
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(r, destDir, chmodX)
	default:
		if filename == "" {
			filename = "artifact"
		}
		out := filepath.Join(destDir, filename)
		mode := os.FileMode(0o644)
		if chmodX {
			mode = installExecutableMode(mode)
		}
		f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		if _, err := io.Copy(f, r); err != nil {
			return 0, err
		}
		return 1, nil
	}
}

// stripPrefix removes a common leading path segment shared by all
// entries (e.g. "fzf-0.54.0/") so extraction is flat.
func stripPrefix(entries []string) string {
	if len(entries) == 0 {
		return ""
	}
	first := entries[0]
	slash := strings.IndexByte(first, '/')
	if slash < 0 {
		return ""
	}
	prefix := first[:slash+1]
	for _, e := range entries {
		if !strings.HasPrefix(e, prefix) {
			return ""
		}
	}
	return prefix
}

func extractTarGz(r io.Reader, dest string, chmodX bool) (int, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	// First pass: buffer all entries to find common prefix. For
	// simplicity we stream and strip on a per-path basis using the
	// first entry's root.
	tr := tar.NewReader(gz)
	var names []string
	type entry struct {
		hdr  *tar.Header
		body []byte
	}
	var entries []entry
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			continue
		}
		buf, err := io.ReadAll(tr)
		if err != nil {
			return 0, err
		}
		entries = append(entries, entry{h, buf})
		names = append(names, h.Name)
	}
	prefix := stripPrefix(names)
	count := 0
	for _, e := range entries {
		rel := strings.TrimPrefix(e.hdr.Name, prefix)
		if rel == "" {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return count, err
		}
		mode := os.FileMode(e.hdr.Mode) & 0o777
		if mode == 0 {
			mode = 0o644
		}
		if chmodX {
			mode = installExecutableMode(mode)
		}
		if err := os.WriteFile(target, e.body, mode); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func extractZip(r io.Reader, dest string, chmodX bool) (int, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	zr, err := zip.NewReader(byteReader(buf), int64(len(buf)))
	if err != nil {
		return 0, fmt.Errorf("zip: %w", err)
	}
	var names []string
	for _, f := range zr.File {
		if f.Mode().IsRegular() {
			names = append(names, f.Name)
		}
	}
	prefix := stripPrefix(names)
	count := 0
	for _, f := range zr.File {
		if !f.Mode().IsRegular() {
			continue
		}
		rel := strings.TrimPrefix(f.Name, prefix)
		if rel == "" {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return count, err
		}
		rc, err := f.Open()
		if err != nil {
			return count, err
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		if chmodX {
			mode = installExecutableMode(mode)
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			rc.Close()
			return count, err
		}
		if _, err := io.Copy(out, rc); err != nil {
			rc.Close()
			out.Close()
			return count, err
		}
		rc.Close()
		out.Close()
		count++
	}
	return count, nil
}

// byteReader adapts []byte into io.ReaderAt expected by zip.NewReader.
type byteReaderT struct{ b []byte }

func (b byteReaderT) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(b.b)) {
		return 0, io.EOF
	}
	n := copy(p, b.b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
func byteReader(b []byte) byteReaderT { return byteReaderT{b} }

func parseRemote(target string) (string, error) {
	if !strings.HasPrefix(target, "remote://") {
		return "", fmt.Errorf("target must start with remote:// (got: %s)", target)
	}
	p := strings.TrimPrefix(target, "remote://")
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
		return "", fmt.Errorf("bad path: %s", p)
	}
	return p, nil
}

func cmdRun(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: tool_cli run remote://<path> [args...]")
	}
	target := args[0]
	scriptArgs := args[1:]
	p, err := parseRemote(target)
	if err != nil {
		return err
	}
	base, err := currentURL()
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/get_script?path=%s", base, url.QueryEscape(p))
	body, _, err := friendlyFetch(u)
	if err != nil {
		var fe *fetchErr
		if errors.As(err, &fe) {
			switch fe.Code() {
			case 404:
				return fmt.Errorf("tool_cli run: script not found: remote://%s", p)
			case 400:
				return fmt.Errorf("tool_cli run: invalid script path: remote://%s", p)
			case 0:
				return fmt.Errorf("tool_cli run: %s", fe.Error())
			default:
				return fmt.Errorf("tool_cli run: server returned HTTP %d for remote://%s", fe.Code(), p)
			}
		}
		return fmt.Errorf("tool_cli run: %s", err.Error())
	}
	defer body.Close()
	return runShellWithStdin(body, scriptArgs)
}

func cmdPush(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: tool_cli push <local> remote://<path>")
	}
	local := args[0]
	p, err := parseRemote(args[1])
	if err != nil {
		return err
	}
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()
	base, err := currentURL()
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/put_script?path=%s", base, url.QueryEscape(p))
	req, _ := http.NewRequest("PUT", u, f)
	st, _ := f.Stat()
	if st != nil {
		req.ContentLength = st.Size()
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("push failed: HTTP %d", resp.StatusCode)
	}
	return nil
}

// ---------- dispatch ----------

func main() {
	if len(os.Args) < 2 {
		_ = cmdHelp(nil)
		os.Exit(1)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "help", "-h", "--help":
		err = cmdHelp(args)
	case "set-url":
		err = cmdSetURL(args)
	case "config":
		err = cmdConfig(args)
	case "url":
		err = cmdURL(args)
	case "ping":
		err = cmdPing(args)
	case "get":
		err = cmdGet(args)
	case "install":
		err = cmdInstall(args)
	case "run":
		err = cmdRun(args)
	case "push":
		err = cmdPush(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		_ = cmdHelp(nil)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runShellWithStdin(stdin io.Reader, scriptArgs []string) error {
	cmdArgs := append([]string{"-s", "--"}, scriptArgs...)
	c := exec.Command("sh", cmdArgs...)
	c.Stdin = stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
