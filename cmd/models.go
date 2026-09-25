package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/azzenabidi/acestep-cli/pkg/downloader"
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "Inspect and manage the downloaded GGUF models",
	Args:  cobra.NoArgs,
}

var modelsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the models on disk",
	Args:  cobra.NoArgs,
	RunE:  runModelsList,
}

var modelsFetchCmd = &cobra.Command{
	Use:   "fetch <preset|role|file.gguf>...",
	Short: "Download models without running setup",
	Long: `Download a model bundle, a whole role, or individual GGUF files.

  acestep models fetch lowvram
  acestep models fetch dit
  acestep models fetch acestep-v15-sft-Q8_0.gguf
  acestep models fetch lm,dit,vae

Roles are lm, dit, vae and text-encoder. Already-present files are skipped
unless --force is given.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runModelsFetch,
}

var modelsRemoveCmd = &cobra.Command{
	Use:   "remove <file.gguf>...",
	Short: "Delete model files",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runModelsRemove,
}

var modelsPresetsCmd = &cobra.Command{
	Use:   "presets",
	Short: "Show the model bundles and their sizes",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		printPresets()
		return nil
	},
}

var forceFetch bool

func init() {
	modelsFetchCmd.Flags().BoolVarP(&forceFetch, "force", "f", false, "re-download files that are already present")
	modelsCmd.AddCommand(modelsListCmd, modelsFetchCmd, modelsRemoveCmd, modelsPresetsCmd)
	rootCmd.AddCommand(modelsCmd)
}

// modelFile is one row of `models list`.
type modelFile struct {
	Name string
	Size int64
	Path string
}

func scanModels() []modelFile {
	entries, err := os.ReadDir(global.layout.Models)
	if err != nil {
		return nil
	}
	var out []modelFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".gguf") {
			continue
		}
		size := int64(0)
		if fi, err := e.Info(); err == nil {
			size = fi.Size()
		}
		out = append(out, modelFile{Name: e.Name(), Size: size, Path: filepath.Join(global.layout.Models, e.Name())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func runModelsList(_ *cobra.Command, _ []string) error {
	files := scanModels()
	if global.jsonOut {
		return emitJSON(map[string]any{
			"dir":    global.layout.Models,
			"count":  len(files),
			"models": files,
		})
	}
	heading(fmt.Sprintf("Models in %s", global.layout.Models))
	if len(files) == 0 {
		hint("No models yet. Run `acestep setup --models lowvram` for a small bundle.")
		return nil
	}
	var total int64
	for _, f := range files {
		role := roleOf(f.Name)
		fmt.Fprintf(stdout(), "  %-44s %10s  %s\n", f.Name, humanBytes(f.Size), dim(role))
		total += f.Size
	}
	fmt.Fprintln(stdout())
	info("  %d file(s), %s total", len(files), humanBytes(total))
	return nil
}

// roleOf classifies a GGUF filename by the pipeline stage it serves.
func roleOf(name string) string {
	switch {
	case strings.Contains(name, "Embedding"):
		return "text-encoder"
	case strings.Contains(name, "vae"):
		return "vae"
	case strings.Contains(name, "lm-"):
		return "lm"
	case strings.Contains(name, "v15-") || strings.Contains(name, "v15-") || strings.Contains(name, "v15"):
		return "dit"
	}
	return "unknown"
}

func runModelsFetch(cmd *cobra.Command, args []string) error {
	if err := global.layout.EnsureDirs(); err != nil {
		return err
	}
	ctx := signalContext()
	d := downloader.New(global.viper.GetString("models.repo"), global.layout.Models)
	d.Force = forceFetch
	d.Progress = !global.jsonOut && isInteractive()
	d.Quiet = global.quiet

	var models []downloader.Model
	for _, arg := range args {
		resolved, err := downloader.Resolve(arg)
		if err != nil {
			return err
		}
		models = append(models, resolved...)
	}
	if err := fetchModels(ctx, models); err != nil {
		return err
	}
	if global.jsonOut {
		return emitJSON(map[string]any{"fetched": len(models), "dir": global.layout.Models})
	}
	return nil
}

func runModelsRemove(_ *cobra.Command, args []string) error {
	heading("Removing models")
	var removed []string
	for _, arg := range args {
		name := filepath.Base(arg)
		path := filepath.Join(global.layout.Models, name)
		if err := os.Remove(path); err != nil {
			fail("%s: %v", name, err)
			continue
		}
		success("removed %s", name)
		removed = append(removed, name)
	}
	if global.jsonOut {
		return emitJSON(map[string]any{"removed": removed})
	}
	return nil
}
