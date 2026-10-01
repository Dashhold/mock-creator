package render

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	// ErrPDFUnavailable means this server has no typst binary.
	ErrPDFUnavailable = errors.New("PDF export is not available on this server")
	// ErrBusy means every render slot stayed taken for the whole wait.
	ErrBusy = errors.New("the PDF renderer is busy, try again in a moment")
)

// PDFRenderer runs the Typst CLI against the embedded template. One root
// directory holds the template, logo and fonts; each render gets its own job
// folder for the data file, removed afterwards.
type PDFRenderer struct {
	bin     string
	version string
	root    string
	slots   chan struct{}
	timeout time.Duration
}

var (
	pdfOnce sync.Once
	pdfInst *PDFRenderer
	pdfErr  error
)

// DefaultPDF returns the process-wide renderer, built on first use. TYPST_BIN
// overrides the binary (default: typst on PATH).
func DefaultPDF() (*PDFRenderer, error) {
	pdfOnce.Do(func() {
		bin := os.Getenv("TYPST_BIN")
		if bin == "" {
			bin = "typst"
		}
		pdfInst, pdfErr = NewPDFRenderer(bin, 2, 90*time.Second)
	})
	return pdfInst, pdfErr
}

// NewPDFRenderer writes the assets to a temp directory and checks the binary.
func NewPDFRenderer(bin string, concurrency int, timeout time.Duration) (*PDFRenderer, error) {
	resolved, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPDFUnavailable, err)
	}
	root, err := writeAssets()
	if err != nil {
		return nil, fmt.Errorf("prepare render assets: %w", err)
	}
	r := &PDFRenderer{bin: resolved, root: root, slots: make(chan struct{}, max(1, concurrency)), timeout: timeout}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := r.command(ctx, "--version").Output()
	if err != nil {
		return nil, fmt.Errorf("%w: typst --version: %v", ErrPDFUnavailable, err)
	}
	r.version = strings.TrimSpace(string(out))
	return r, nil
}

// Version reports the typst version line, e.g. "typst 0.15.1 (...)".
func (r *PDFRenderer) Version() string { return r.version }

// Render produces the PDF for a document.
func (r *PDFRenderer) Render(ctx context.Context, doc Document) ([]byte, error) {
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-time.After(20 * time.Second):
		return nil, ErrBusy
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	data, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encode print model: %w", err)
	}
	job, err := randomID()
	if err != nil {
		return nil, err
	}
	jobDir := filepath.Join(r.root, "jobs", job)
	if err := os.MkdirAll(jobDir, 0o700); err != nil {
		return nil, err
	}
	defer os.RemoveAll(jobDir)
	if err := os.WriteFile(filepath.Join(jobDir, "data.json"), data, 0o600); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	cmd := r.command(ctx, "compile",
		"--root", r.root,
		"--font-path", filepath.Join(r.root, "fonts"),
		"--ignore-system-fonts",
		"--package-path", filepath.Join(r.root, "packages"),
		"--package-cache-path", filepath.Join(r.root, "packages"),
		"--creation-timestamp", "1790000000",
		"--jobs", "2",
		"--diagnostic-format", "short",
		// The data path is relative to --root and uses forward slashes on every OS.
		"--input", "data="+path.Join("jobs", job, "data.json"),
		"paper.typ", "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("PDF render timed out after %s", r.timeout)
		}
		return nil, fmt.Errorf("PDF render failed: %v: %s", err, trim(stderr.String(), 600))
	}
	pdf := stdout.Bytes()
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		return nil, fmt.Errorf("PDF render produced no PDF: %s", trim(stderr.String(), 600))
	}
	return pdf, nil
}

// command runs typst in the render root with every TYPST_* variable removed,
// so the host environment cannot redirect fonts, packages or the root.
func (r *PDFRenderer) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.bin, args...)
	cmd.Dir = r.root
	env := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "TYPST_") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	return cmd
}

// writeAssets lays out <tmp>/mockcreator-render-<hash>/ with paper.typ,
// logo.png, fonts/, jobs/ and an empty packages/ (so a template can never
// fetch a package over the network).
func writeAssets() (string, error) {
	names := []string{"assets/paper.typ", "assets/logo.png"}
	fonts, err := fontFiles()
	if err != nil {
		return "", err
	}
	names = append(names, fonts...)

	h := sha256.New()
	contents := map[string][]byte{}
	for _, n := range names {
		b, err := assets.ReadFile(n)
		if err != nil {
			return "", err
		}
		contents[n] = b
		h.Write([]byte(n))
		h.Write(b)
	}
	root := filepath.Join(os.TempDir(), "mockcreator-render-"+hex.EncodeToString(h.Sum(nil))[:12])
	for _, dir := range []string{root, filepath.Join(root, "fonts"), filepath.Join(root, "jobs"), filepath.Join(root, "packages")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
	}
	for _, n := range names {
		dst := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(n, "assets/")))
		if st, err := os.Stat(dst); err == nil && st.Size() == int64(len(contents[n])) {
			continue
		}
		tmp := dst + ".tmp"
		if err := os.WriteFile(tmp, contents[n], 0o600); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, dst); err != nil {
			return "", err
		}
	}
	return root, nil
}

func randomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
