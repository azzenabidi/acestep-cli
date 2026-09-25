package downloader

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPresetLookup(t *testing.T) {
	for _, name := range PresetNames() {
		if _, ok := PresetByName(name); !ok {
			t.Errorf("PresetByName(%q) failed for a name in PresetNames()", name)
		}
	}
	// Lookup is case-insensitive so `--models LowVRAM` works.
	if _, ok := PresetByName("LOWVRAM"); !ok {
		t.Error("preset lookup should be case-insensitive")
	}
	if _, ok := PresetByName("nonexistent"); ok {
		t.Error("unknown preset should not resolve")
	}
}

// Every preset must contain exactly one model per pipeline role, otherwise
// setup would fetch a bundle that cannot run.
func TestPresetsCoverAllRoles(t *testing.T) {
	want := map[Role]bool{RoleLM: true, RoleTextEncode: true, RoleDiT: true, RoleVAE: true}
	for _, p := range Presets {
		seen := map[Role]bool{}
		for _, m := range p.Models {
			seen[m.Role] = true
			if m.Name == "" || !strings.HasSuffix(m.Name, ".gguf") {
				t.Errorf("preset %s: model %q is not a .gguf filename", p.Name, m.Name)
			}
		}
		for role := range want {
			if !seen[role] {
				t.Errorf("preset %s is missing the %s role", p.Name, role)
			}
		}
	}
}

func TestResolve(t *testing.T) {
	t.Run("preset", func(t *testing.T) {
		got, err := Resolve("lowvram")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		want := len(Presets[0].Models)
		if len(got) != want {
			t.Errorf("got %d models, want %d", len(got), want)
		}
	})

	t.Run("multiple presets dedupe", func(t *testing.T) {
		// lowvram and essential share the text encoder and the VAE.
		got, err := Resolve("lowvram,essential")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		seen := map[string]bool{}
		for _, m := range got {
			if seen[m.Name] {
				t.Errorf("duplicate model %s", m.Name)
			}
			seen[m.Name] = true
		}
	})

	t.Run("role", func(t *testing.T) {
		got, err := Resolve("vae")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if len(got) == 0 {
			t.Fatal("role resolution returned nothing")
		}
		for _, m := range got {
			if m.Role != RoleVAE {
				t.Errorf("role vae resolved to %s (role %s)", m.Name, m.Role)
			}
		}
	})

	t.Run("explicit filename", func(t *testing.T) {
		got, err := Resolve("acestep-v15-sft-Q8_0.gguf")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if len(got) != 1 || got[0].Name != "acestep-v15-sft-Q8_0.gguf" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("errors", func(t *testing.T) {
		for _, spec := range []string{"", "nonsense", "not-a-model"} {
			if _, err := Resolve(spec); err == nil {
				t.Errorf("Resolve(%q) should fail", spec)
			}
		}
	})
}

func TestFileURL(t *testing.T) {
	d := New("", "")
	if d.Repo != DefaultRepo {
		t.Errorf("default repo = %q, want %q", d.Repo, DefaultRepo)
	}
	got := d.FileURL("vae-BF16.gguf")
	want := "https://huggingface.co/" + DefaultRepo + "/resolve/main/vae-BF16.gguf?download=true"
	if got != want {
		t.Errorf("FileURL = %q, want %q", got, want)
	}
}

func TestTotalSize(t *testing.T) {
	if got := TotalSize(nil); got != 0 {
		t.Errorf("TotalSize(nil) = %d, want 0", got)
	}
	p := Presets[0]
	var want int64
	for _, m := range p.Models {
		want += m.Size
	}
	if got := TotalSize(p.Models); got != want {
		t.Errorf("TotalSize = %d, want %d", got, want)
	}
}

// TestFetchStreamsAndPublishes checks that bytes land in a .part file and are
// only renamed into place on success, so an interrupted pull never leaves a
// truncated GGUF behind.
func TestFetchStreamsAndPublishes(t *testing.T) {
	payload := strings.Repeat("gguf-bytes-", 1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	dir := t.TempDir()
	d := New("owner/repo", dir)
	d.Progress = false
	d.Quiet = true
	// Point the downloader at the test server.
	d.BaseURL = srv.URL

	m := Model{Name: "model.gguf", Role: RoleDiT, Size: int64(len(payload))}
	path, err := d.Fetch(context.Background(), m)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if filepath.Base(path) != "model.gguf" {
		t.Errorf("path = %s", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != payload {
		t.Errorf("content mismatch: got %d bytes, want %d", len(got), len(payload))
	}
	// The .part file must be gone after a successful publish.
	if _, err := os.Stat(path + partSuffix); !os.IsNotExist(err) {
		t.Error(".part file was left behind after a successful download")
	}
}

func TestFetchSkipsExistingFile(t *testing.T) {
	dir := t.TempDir()
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte("data"))
	}))
	defer srv.Close()

	m := Model{Name: "model.gguf", Role: RoleVAE}
	if err := os.WriteFile(filepath.Join(dir, m.Name), []byte("already here"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := New("owner/repo", dir)
	d.Progress = false
	if _, err := d.Fetch(context.Background(), m); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if hits != 0 {
		t.Errorf("server was contacted %d time(s) for an existing file", hits)
	}
}

func TestFetchForceRedownloads(t *testing.T) {
	dir := t.TempDir()
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			hits++
		}
		_, _ = w.Write([]byte("fresh"))
	}))
	defer srv.Close()

	m := Model{Name: "model.gguf", Role: RoleVAE}
	if err := os.WriteFile(filepath.Join(dir, m.Name), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := New("owner/repo", dir)
	d.Progress = false
	d.Force = true
	d.BaseURL = srv.URL
	if _, err := d.Fetch(context.Background(), m); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if hits != 1 {
		t.Errorf("server hit %d times, want 1", hits)
	}
	got, err := os.ReadFile(filepath.Join(dir, m.Name))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "fresh" {
		t.Errorf("content = %q, want %q", got, "fresh")
	}
}

// A server that ignores Range must restart the download rather than appending
// to the partial file and corrupting it.
func TestFetchRestartsWhenRangeIgnored(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deliberately answer 200 even when a Range header is present.
		_, _ = w.Write([]byte("ABCDEFGH"))
	}))
	defer srv.Close()

	m := Model{Name: "model.gguf", Role: RoleVAE}
	part := filepath.Join(dir, m.Name+partSuffix)
	if err := os.WriteFile(part, []byte("XXXX"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := New("owner/repo", dir)
	d.Progress = false
	d.Quiet = true
	d.BaseURL = srv.URL
	path, err := d.Fetch(context.Background(), m)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ABCDEFGH" {
		t.Errorf("content = %q, want %q (partial bytes were not discarded)", got, "ABCDEFGH")
	}
}

func TestFetchResumesWhenRangeHonoured(t *testing.T) {
	dir := t.TempDir()
	const full = "ABCDEFGHIJ"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHdr := r.Header.Get("Range")
		if rangeHdr == "" {
			_, _ = w.Write([]byte(full))
			return
		}
		var start int
		if _, err := fmt.Sscanf(rangeHdr, "bytes=%d-", &start); err != nil {
			t.Errorf("unparsable Range %q", rangeHdr)
		}
		w.Header().Set("Content-Range", "bytes "+rangeHdr+"-"+strconv.Itoa(len(full)-1)+"/"+strconv.Itoa(len(full)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte(full[start:]))
	}))
	defer srv.Close()

	m := Model{Name: "model.gguf", Role: RoleVAE}
	part := filepath.Join(dir, m.Name+partSuffix)
	if err := os.WriteFile(part, []byte(full[:4]), 0o644); err != nil {
		t.Fatal(err)
	}

	d := New("owner/repo", dir)
	d.Progress = false
	d.Quiet = true
	d.BaseURL = srv.URL
	path, err := d.Fetch(context.Background(), m)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != full {
		t.Errorf("content = %q, want %q", got, full)
	}
}

func TestFetchPropagatesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	d := New("owner/repo", t.TempDir())
	d.Progress = false
	d.BaseURL = srv.URL
	_, err := d.Fetch(context.Background(), Model{Name: "nope.gguf"})
	if err == nil {
		t.Fatal("expected an error for a 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error %q should mention the status", err)
	}
}

func TestInspect(t *testing.T) {
	dir := t.TempDir()
	d := New("owner/repo", dir)
	m := Model{Name: "m.gguf", Size: 100}

	s := d.Inspect(m)
	if s.Present || s.Complete {
		t.Error("a missing model should not report present")
	}
	if err := os.WriteFile(filepath.Join(dir, m.Name), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	s = d.Inspect(m)
	if !s.Present || !s.Complete {
		t.Error("an existing model should report present and complete")
	}
	if s.Size != 10 {
		t.Errorf("size = %d, want 10 (actual file size, not the hint)", s.Size)
	}
}

func TestExistingSize(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope")
	if got, err := existingSize(missing); err != nil || got != 0 {
		t.Errorf("existingSize(missing) = %d, %v; want 0, nil", got, err)
	}
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := existingSize(path); err != nil || got != 5 {
		t.Errorf("existingSize = %d, %v; want 5, nil", got, err)
	}
	if _, err := existingSize(dir); err == nil {
		t.Error("existingSize on a directory should error")
	}
}
