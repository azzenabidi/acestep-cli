package main

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	versionShort bool
	versionJSON  bool
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		if versionJSON || global.jsonOut {
			return emitJSON(struct {
				Version    string `json:"version"`
				Commit     string `json:"commit"`
				Date       string `json:"date"`
				Go         string `json:"go"`
				Platform   string `json:"platform"`
				ACESTEPCPP string `json:"acestep_cpp"`
			}{
				Version:    version,
				Commit:     commit,
				Date:       date,
				Go:         runtime.Version(),
				Platform:   runtime.GOOS + "/" + runtime.GOARCH,
				ACESTEPCPP: engineRepo,
			})
		}
		if versionShort {
			fmt.Fprintln(stdout(), version)
			return nil
		}
		heading("acestep " + bold(version))
		info("  %-12s %s", dim("commit"), commit)
		info("  %-12s %s", dim("built"), date)
		info("  %-12s %s", dim("go"), runtime.Version())
		info("  %-12s %s", dim("platform"), runtime.GOOS+"/"+runtime.GOARCH)
		info("  %-12s %s", dim("engine"), engineRepo)
		info("  %-12s %s", dim("root"), global.layout.Root)
		return nil
	},
}

// doctorCmd diagnoses a broken installation, which is the first thing to run
// when generation fails on a new machine.
var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check the installation and report problems",
	Args:  cobra.NoArgs,
	RunE:  runDoctor,
}

func init() {
	versionCmd.Flags().BoolVar(&versionShort, "short", false, "print just the version string")
	versionCmd.Flags().BoolVar(&versionJSON, "json", false, "print version information as JSON")
	rootCmd.AddCommand(versionCmd, doctorCmd)
}

// check is one diagnostic result.
type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

func runDoctor(_ *cobra.Command, _ []string) error {
	var checks []check

	add := func(name string, ok bool, detail, fix string) {
		checks = append(checks, check{Name: name, OK: ok, Detail: detail, Fix: fix})
	}

	// Directories.
	for _, dir := range []struct {
		name string
		path string
	}{
		{"root", global.layout.Root},
		{"bin", global.layout.Bin},
		{"models", global.layout.Models},
		{"outputs", global.layout.Outputs},
		{"work", global.layout.Work},
	} {
		info2, err2 := dirStatus(dir.path)
		add("dir:"+dir.name, err2 == nil, info2, "acestep setup")
	}

	// Engine binaries.
	for _, name := range engineBinaries {
		path, ok := binStatus(global.layout.Bin, name)
		add("engine:"+name, ok, path, "acestep setup --skip-binaries")
	}

	// Models by role.
	models := scanModels()
	add("models:count", len(models) > 0, fmt.Sprintf("%d .gguf file(s)", len(models)), "acestep setup")
	for _, role := range []string{"lm", "dit", "vae", "text-encoder"} {
		found := 0
		for _, m := range models {
			if roleOf(m.Name) == role {
				found++
			}
		}
		add("models:"+role, found > 0, fmt.Sprintf("%d", found), "acestep models fetch "+role)
	}

	// Free space on the models volume, since the weights are multi-gigabyte.
	if free, err := freeSpace(global.layout.Root); err == nil {
		ok := free > 2<<30
		detail := humanBytes(int64(free)) + " free"
		fix := ""
		if !ok {
			fix = "free up disk space; the weights need several GB"
		}
		add("disk:free", ok, detail, fix)
	}

	// Hardware hints for the common failure mode: too little VRAM/RAM.
	add("hardware:memory", true, memoryHint(), "")

	problems := 0
	for _, c := range checks {
		if c.OK {
			success("%-22s %s", c.Name, dim(c.Detail))
		} else {
			problems++
			fail("%-22s %s", c.Name, c.Detail)
			if c.Fix != "" {
				hint("  fix: %s", c.Fix)
			}
		}
	}

	if global.jsonOut {
		if err := emitJSON(map[string]any{"checks": checks, "problems": problems}); err != nil {
			return err
		}
	} else {
		info("")
		if problems == 0 {
			success("everything looks good")
		} else {
			info("%d problem(s) found", problems)
		}
	}
	if problems > 0 {
		return errorf("%d problem(s) found", problems)
	}
	return nil
}

func dirStatus(path string) (string, error) {
	_, err := statDir(path)
	if err != nil {
		return "missing", err
	}
	return path, nil
}

func binStatus(dir, name string) (string, bool) {
	path := filepath.Join(dir, name)
	if !fileExists(path) {
		return "missing at " + filepath.Join(dir, name), false
	}
	if !executable(path) {
		return "not executable at " + filepath.Join(dir, name), false
	}
	return path, true
}

func memoryHint() string {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	// runtime only reports what this process used; the useful number is the
	// machine total, which we read from /proc on Linux.
	if total := totalMemory(); total > 0 {
		return fmt.Sprintf("%.0f GB system RAM", float64(total)/(1<<30))
	}
	return "unknown"
}
