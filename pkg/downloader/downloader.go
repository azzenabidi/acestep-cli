// Package downloader fetches pre-quantized GGUF weights from Hugging Face.
//
// Downloads are resumable: bytes land in a .part file and only the final
// rename publishes the model, so an interrupted pull never leaves a
// truncated GGUF that would load as a corrupt model.
package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/schollz/progressbar/v3"

	"github.com/azzenabidi/acestep-cli/pkg/config"
)

// DefaultRepo is the community quantization suite published for acestep.cpp.
const DefaultRepo = "Serveurperso/ACE-Step-1.5-GGUF"

// partSuffix marks an in-flight download.
const partSuffix = ".part"

// Model is a single downloadable artifact.
type Model struct {
	// Name is the GGUF filename as it appears in the repo.
	Name string
	// Role groups models that serve the same purpose across quantizations.
	Role Role
	// Size is the approximate download size, used only for planning output.
	Size int64
	// Description is a short human-readable purpose line.
	Description string
}

// Role identifies the pipeline stage a model serves.
type Role string

const (
	RoleLM         Role = "lm"
	RoleTextEncode Role = "text-encoder"
	RoleDiT        Role = "dit"
	RoleVAE        Role = "vae"
)

// Preset is a named bundle of models sized for a class of machine.
type Preset struct {
	Name        string
	Description string
	ApproxSize  string
	Models      []Model
}

// Presets are ordered from smallest to largest. The names match the model
// tiers documented by acestep.cpp.
var Presets = []Preset{
	{
		Name:        "lowvram",
		Description: "0.6B LM + turbo DiT at Q4_K_M. Runs in ~4 GB of RAM on CPU.",
		ApproxSize:  "~3.4 GB",
		Models: []Model{
			{Name: "Qwen3-Embedding-0.6B-Q8_0.gguf", Role: RoleTextEncode, Size: 748 << 20, Description: "text encoder (28L, H=1024)"},
			{Name: "acestep-5Hz-lm-0.6B-Q5_K_M.gguf", Role: RoleLM, Size: 640 << 20, Description: "planner LM, Qwen3 0.6B"},
			{Name: "acestep-v15-turbo-Q4_K_M.gguf", Role: RoleDiT, Size: 1500 << 20, Description: "DiT 2B + CondEncoder (24L, H=2048)"},
			{Name: "vae-BF16.gguf", Role: RoleVAE, Size: 322 << 20, Description: "AutoencoderOobleck"},
		},
	},
	{
		Name:        "standard",
		Description: "1.7B LM + turbo DiT at Q5_K_M. Good balance for 8 GB systems.",
		ApproxSize:  "~4.5 GB",
		Models: []Model{
			{Name: "Qwen3-Embedding-0.6B-Q8_0.gguf", Role: RoleTextEncode, Size: 748 << 20, Description: "text encoder (28L, H=1024)"},
			{Name: "acestep-5Hz-lm-1.7B-Q5_K_M.gguf", Role: RoleLM, Size: 1600 << 20, Description: "planner LM, Qwen3 1.7B"},
			{Name: "acestep-v15-turbo-Q5_K_M.gguf", Role: RoleDiT, Size: 1900 << 20, Description: "DiT 2B + CondEncoder (24L, H=2048)"},
			{Name: "vae-BF16.gguf", Role: RoleVAE, Size: 322 << 20, Description: "AutoencoderOobleck"},
		},
	},
	{
		Name:        "essential",
		Description: "4B LM + turbo DiT at Q8_0. Upstream's default Q8_0 set (~7.7 GB).",
		ApproxSize:  "~7.7 GB",
		Models: []Model{
			{Name: "Qwen3-Embedding-0.6B-Q8_0.gguf", Role: RoleTextEncode, Size: 748 << 20, Description: "text encoder (28L, H=1024)"},
			{Name: "acestep-5Hz-lm-4B-Q8_0.gguf", Role: RoleLM, Size: 4200 << 20, Description: "planner LM, Qwen3 4B"},
			{Name: "acestep-v15-turbo-Q8_0.gguf", Role: RoleDiT, Size: 2400 << 20, Description: "DiT 2B + CondEncoder (24L, H=2048)"},
			{Name: "vae-BF16.gguf", Role: RoleVAE, Size: 322 << 20, Description: "AutoencoderOobleck"},
		},
	},
	{
		Name:        "quality",
		Description: "4B LM + sft DiT at Q8_0 (50 steps). Slower, noticeably better output.",
		ApproxSize:  "~12 GB",
		Models: []Model{
			{Name: "Qwen3-Embedding-0.6B-Q8_0.gguf", Role: RoleTextEncode, Size: 748 << 20, Description: "text encoder (28L, H=1024)"},
			{Name: "acestep-5Hz-lm-4B-Q8_0.gguf", Role: RoleLM, Size: 4200 << 20, Description: "planner LM, Qwen3 4B"},
			{Name: "acestep-v15-sft-Q8_0.gguf", Role: RoleDiT, Size: 3400 << 20, Description: "DiT SFT, 50 steps, higher quality"},
			{Name: "vae-BF16.gguf", Role: RoleVAE, Size: 322 << 20, Description: "AutoencoderOobleck"},
		},
	},
}

// PresetByName looks up a preset, case-insensitively.
func PresetByName(name string) (Preset, bool) {
	for _, p := range Presets {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Preset{}, false
}

// PresetNames lists the available preset names in order.
func PresetNames() []string {
	names := make([]string, 0, len(Presets))
	for _, p := range Presets {
		names = append(names, p.Name)
	}
	return names
}

// Resolve turns a user-supplied spec into a model list. Accepted forms:
//
//	lowvram                     a preset name
//	essential,lowvram           comma-separated presets (union, deduplicated)
//	dit                         every model serving the dit role
//	lm:Q8_0                     a role or a filename substring
//	acestep-v15-turbo-Q8_0.gguf an exact filename
func Resolve(spec string) ([]Model, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, errors.New("empty model spec")
	}
	var out []Model
	seen := map[string]bool{}
	add := func(m Model) {
		if seen[m.Name] {
			return
		}
		seen[m.Name] = true
		out = append(out, m)
	}

	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, ok := PresetByName(part); ok {
			for _, m := range p.Models {
				add(m)
			}
			continue
		}
		role := strings.ToLower(part)
		if role == string(RoleLM) || role == string(RoleDiT) || role == string(RoleVAE) || role == string(RoleTextEncode) {
			found := false
			for _, p := range Presets {
				for _, m := range p.Models {
					if m.Role == Role(role) {
						add(m)
						found = true
					}
				}
			}
			if !found {
				return nil, fmt.Errorf("no known model for role %q", part)
			}
			continue
		}
		if !strings.HasSuffix(part, ".gguf") {
			return nil, fmt.Errorf("unknown preset or role %q (presets: %s)", part, strings.Join(PresetNames(), ", "))
		}
		add(Model{Name: part, Description: "explicitly requested"})
	}
	if len(out) == 0 {
		return nil, errors.New("model spec selected nothing")
	}
	return out, nil
}

// DefaultBaseURL is the Hugging Face endpoint. Point BaseURL at a mirror
// (for example https://hf-mirror.com) to fetch models from a different host.
const DefaultBaseURL = "https://huggingface.co"

// Downloader pulls models into a target directory.
type Downloader struct {
	Repo string
	// BaseURL is the scheme and host serving the repo.
	BaseURL string
	Dir     string
	Client  *http.Client
	Token   string
	// Force re-downloads files that are already present.
	Force bool
	// Progress draws a progress bar.
	Progress bool
	// Quiet suppresses progress output entirely, for non-interactive use.
	Quiet bool
	// Status receives one-line status messages. Defaults to os.Stderr.
	Status io.Writer
}

// New returns a Downloader with sane HTTP defaults.
func New(repo, dir string) *Downloader {
	if repo == "" {
		repo = DefaultRepo
	}
	return &Downloader{
		Repo:     repo,
		BaseURL:  DefaultBaseURL,
		Dir:      dir,
		Client:   &http.Client{Timeout: 0},
		Progress: true,
		Status:   os.Stderr,
	}
}

// FileURL builds the canonical Hugging Face resolve URL. The download=true
// query makes the CDN serve an attachment rather than an HTML preview page.
func (d *Downloader) FileURL(name string) string {
	base := d.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	return fmt.Sprintf("%s/%s/resolve/main/%s?download=true",
		strings.TrimRight(base, "/"), d.Repo, url.PathEscape(name))
}

// statusf writes a one-line status message unless output is suppressed.
func (d *Downloader) statusf(format string, args ...any) {
	if d.Quiet {
		return
	}
	w := d.Status
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, format+"\n", args...)
}

// Status describes what a model needs before it can be used.
type Status struct {
	Model    Model
	Path     string
	Present  bool
	Size     int64
	Complete bool
}

// Inspect checks a model on disk without touching the network.
func (d *Downloader) Inspect(m Model) Status {
	path := filepath.Join(d.Dir, m.Name)
	s := Status{Model: m, Path: path, Size: m.Size}
	if fi, err := os.Stat(path); err == nil {
		s.Present = true
		s.Size = fi.Size()
		s.Complete = fi.Size() > 0
	}
	return s
}

// Fetch downloads one model unless it is already complete. It returns the
// final path on disk.
func (d *Downloader) Fetch(ctx context.Context, m Model) (string, error) {
	if !d.Force {
		if s := d.Inspect(m); s.Present && s.Complete {
			return s.Path, nil
		}
	}
	if err := os.MkdirAll(d.Dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", d.Dir, err)
	}

	final := filepath.Join(d.Dir, m.Name)
	part := final + partSuffix
	from, err := existingSize(part)
	if err != nil {
		return "", err
	}
	total, err := d.ContentLength(ctx, m.Name)
	if err != nil {
		// Not fatal: the GET response carries the authoritative length.
		total = m.Size
	}
	if from > 0 && total > 0 && from < total {
		d.statusf("  resuming %s (%.0f%% of %s already fetched)",
			m.Name, float64(from)/float64(total)*100, humanBytes(total))
	} else if from > 0 && total > 0 && from >= total {
		// The partial file is already complete; publish it rather than
		// re-downloading gigabytes.
		if err := os.Rename(part, final); err != nil {
			return "", fmt.Errorf("publish %s: %w", final, err)
		}
		return final, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.FileURL(m.Name), nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "acestep-cli")
	if from > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", from))
	}
	if d.Token != "" {
		req.Header.Set("Authorization", "Bearer "+d.Token)
	}

	resp, err := d.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", m.Name, err)
	}
	defer resp.Body.Close()

	// 206 = resume accepted. 200 = server ignored Range, restart from zero.
	resuming := resp.StatusCode == http.StatusPartialContent
	if from > 0 && !resuming {
		from = 0
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return "", fmt.Errorf("fetch %s: unexpected status %s", m.Name, resp.Status)
	}

	total = resp.ContentLength
	if resuming {
		total += from
	}

	flags := os.O_CREATE | os.O_WRONLY
	if resuming {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", part, err)
	}
	defer f.Close()

	var bar *progressbar.ProgressBar
	if d.Progress && !d.Quiet && total > 0 {
		bar = progressbar.NewOptions64(
			total,
			progressbar.OptionSetDescription(m.Name),
			progressbar.OptionSetWriter(os.Stderr),
			progressbar.OptionShowBytes(true),
			progressbar.OptionSetWidth(32),
			progressbar.OptionThrottle(120*time.Millisecond),
			progressbar.OptionShowCount(),
			progressbar.OptionOnCompletion(func() {
				fmt.Fprintln(os.Stderr)
			}),
		)
		if from > 0 {
			bar.Add64(from)
		}
	}

	written := from
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return "", fmt.Errorf("write %s: %w", part, werr)
			}
			written += int64(n)
			if bar != nil {
				_ = bar.Add64(int64(n))
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return "", fmt.Errorf("read %s: %w", m.Name, rerr)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("flush %s: %w", part, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close %s: %w", part, err)
	}

	if total > 0 && written != total {
		return "", fmt.Errorf("short read for %s: got %d of %d bytes (re-run to resume)", m.Name, written, total)
	}
	if err := os.Rename(part, final); err != nil {
		return "", fmt.Errorf("publish %s: %w", final, err)
	}
	return final, nil
}

func (d *Downloader) client() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return http.DefaultClient
}

func existingSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	if fi.IsDir() {
		return 0, fmt.Errorf("%s is a directory", path)
	}
	return fi.Size(), nil
}

// TotalSize sums the approximate download size of a model list.
func TotalSize(models []Model) int64 {
	var total int64
	for _, m := range models {
		total += m.Size
	}
	return total
}

// EngineBinary returns the platform-specific name of an engine binary and
// whether it is already present in binDir.
func EngineBinary(binDir, base string) (string, bool) {
	path := filepath.Join(binDir, config.BinaryName(base))
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		return path, true
	}
	return path, false
}

// ContentLength probes a remote model's size without downloading it.
func (d *Downloader) ContentLength(ctx context.Context, name string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, d.FileURL(name), nil)
	if err != nil {
		return 0, err
	}
	if d.Token != "" {
		req.Header.Set("Authorization", "Bearer "+d.Token)
	}
	resp, err := d.client().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HEAD %s: %s", name, resp.Status)
	}
	return strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
}

// humanBytes renders a byte count for progress messages.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
