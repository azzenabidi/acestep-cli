# acestep

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Build](https://github.com/azzenabidi/acestep-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/azzenabidi/acestep-cli/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/azzenabidi/acestep-cli?label=release)](https://github.com/azzenabidi/acestep-cli/releases)
[![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macos%20%7C%20windows-lightgrey.svg)](https://github.com/azzenabidi/acestep-cli/releases)

Generate complete AI songs from a text prompt on your own machine — including
the ones with no GPU.

`acestep` is a thin, well-behaved command-line front end for
[acestep.cpp](https://github.com/ace-step/acestep.cpp), the portable C++17/GGML
implementation of ACE-Step 1.5. It handles model downloads, request
construction, subprocess supervision, live progress, and output naming so you
can go from a prompt to a 48 kHz stereo track with one command.

```
$ acestep generate -p "lofi synthwave with relaxing piano" -d 30
› 1. downloading acestep-5Hz-lm-4B-Q8_0.gguf (4.2 GiB)
  acestep-5Hz-lm-4B-Q8_0.gguf  100% |████████████████| (4.2 GiB/4.2 GiB, 18 MiB/s)

generating
  ace-lm      planning lyrics and audio codes
  ace-synth   rendering audio

✓ wrote ~/Music/acestep/lofi-synthwave-with-relaxing-piano-20260925-142233.mp3
  took 2m41s
```

- **Fully local.** No account, no API key, no telemetry. Prompts, lyrics and
  audio never leave the machine.
- **CPU-first.** Tuned to work on integrated and low-VRAM graphics, including
  2 GB cards like the NVIDIA MX230, and on pure CPU.
- **One static binary.** Cross-compiled for Linux, macOS and Windows with no
  runtime dependencies.
- **Interruptible.** `Ctrl+C` terminates the whole engine process tree instead
  of leaving orphans holding VRAM.

---

## Table of contents

- [Requirements](#requirements)
- [Hardware compatibility](#hardware-compatibility)
- [Install](#install)
- [Quick start](#quick-start)
- [Usage](#usage)
- [Configuration](#configuration)
- [Command reference](#command-reference)
- [How it works](#how-it-works)
- [Troubleshooting](#troubleshooting)
- [Contributing](#contributing)
- [License](#license)
- [Credits](#credits)

---

## Requirements

| | Minimum | Recommended |
|---|---|---|
| **Go** (to build) | 1.25 | 1.25+ |
| **RAM** | 4 GB | 16 GB |
| **VRAM** | none (CPU mode works) | 8 GB+ |
| **Disk** | 4 GB (lowvram bundle) | 15 GB (essential bundle) |
| **Toolchain** (to build the engine) | `git`, `cmake`, a C++17 compiler | CUDA 12.x or Vulkan SDK |

Building `acestep.cpp` also needs a C++17 compiler. On Linux:
`sudo apt install git cmake build-essential` (or `sudo dnf install git cmake gcc-c++`).
On macOS: `xcode-select --install` plus `brew install cmake`.

---

## Hardware compatibility

The engine is memory-hungry: it loads a planner LM, a text encoder, a DiT and a
VAE. Pick the bundle that fits your machine.

| Machine | RAM / VRAM | Bundle | Expected speed for 30 s of audio |
|---|---|---|---|
| **No GPU, 4 GB RAM** | 4 GB | `lowvram` | minutes, CPU only |
| **Integrated, 2 GB VRAM** (e.g. MX230) | 8 GB | `lowvram` + CPU offload | 5–20 min |
| **Dedicated, 4–6 GB VRAM** (e.g. GTX 1650) | 8 GB | `lowvram` | 1–3 min |
| **Dedicated, 8 GB VRAM** (e.g. RTX 3060) | 16 GB | `standard` | 30–60 s |
| **Dedicated, 12 GB+ VRAM** (e.g. RTX 4070) | 32 GB | `essential` / `quality` | 10–30 s |
| **Apple Silicon** | 16 GB unified | `standard` | 30–90 s |
| **Apple Silicon, 32 GB+** | 32 GB+ | `essential` | 15–40 s |

> **MX230 and other 2 GB cards:** keep the GPU out of the critical path. Build
> the engine with `--cpu`, or use Vulkan and let the engine offload to system
> RAM. The 2 GB card cannot hold a useful slice of the 2.4 GB DiT, and
> thrashing VRAM is far slower than a clean CPU run. See
> [docs/HARDWARE_GUIDE.md](docs/HARDWARE_GUIDE.md) for the exact flags.

---

## Install

### With Go

```sh
go install github.com/azzenabidi/acestep-cli/cmd@latest
```

This puts `acestep` in `$(go env GOPATH)/bin`. Make sure that directory is on
your `PATH`.

### From a release

Download the archive for your platform from
[Releases](https://github.com/azzenabidi/acestep-cli/releases) and move the
binary onto your `PATH`:

```sh
# Linux x86_64
curl -fsSL -o acestep.tar.gz \
  https://github.com/azzenabidi/acestep-cli/releases/download/v1.0.0/acestep_1.0.0_Linux_x86_64.tar.gz
tar xzf acestep.tar.gz acestep
sudo install -m755 acestep /usr/local/bin/acestep
```

Or use the installer, which picks the right archive, verifies the published
checksum and handles `PATH` for you:

```sh
curl -fsSL https://raw.githubusercontent.com/azzenabidi/acestep-cli/main/scripts/install.sh | sh
```

Archive names are `acestep_<version>_<Os>_<Arch>`, with `Os` in `Linux`,
`Darwin` or `Windows` and `Arch` in `x86_64` or `arm64`. Substitute the exact
tag from the release page.

### From source

```sh
git clone https://github.com/azzenabidi/acestep-cli.git
cd acestep-cli
make build          # binary lands in bin/
./bin/acestep doctor
```

### One-line setup

After installing the binary, `acestep setup` builds the engine and downloads
the models in one step:

```sh
acestep setup --cuda          # NVIDIA
acestep setup --vulkan        # AMD / Intel
acestep setup                 # CPU only, safe default
```

---

## Quick start

```sh
# 1. build the engine and fetch weights (~8 GB, take your time)
acestep setup --models essential

# 2. confirm everything is in place
acestep doctor

# 3. make a track
acestep generate -p "lofi synthwave with relaxing piano" -d 30
```

Outputs land in `~/Music/acestep` (or the `outputs-dir` you configure).

---

## Usage

### A quick sketch

```sh
acestep generate -p "ambient drone, slow strings, tape hiss" -d 30 --instrumental
```

### Your own lyrics

Pass a file or inline text. Anything that is not a readable path is treated as
lyrics, so quoting a multi-line lyric in your shell works too.

```sh
acestep generate -p "90s alt rock, gritty guitars" -l lyrics.txt -d 180
acestep generate -p "ballad" -l $'Verse one\nHold the line\nHold the line'
```

### No vocals

```sh
acestep generate -p "jazz trio, upright bass" --instrumental
```

`--instrumental` is a shortcut for `lyrics: "[Instrumental]"`, the exact string
the DiT was trained on to mean "no vocals".

### Four variations at once

```sh
acestep generate -p "drum and bass, 174 bpm" -n 4
```

Each variation is a separate song with its own lyrics, metadata and audio
codes. Files are written as `name-01.mp3` … `name-04.mp3`.

### Specific metadata

```sh
acestep generate -p "techno" --bpm 128 --key "F# minor" --time-signature 4 \
  --language ja -d 120 -o midnight-drive.wav
```

### Lossless output

```sh
acestep generate -p "orchestral fanfare" --format wav24
```

`--format` accepts `mp3`, `wav16`, `wav24` and `wav32`. The engine picks the
encoder, not the file extension.

### Reproducible output

```sh
acestep generate -p "lofi" --seed 12345
```

A fixed seed makes the DiT noise deterministic. The planner LM always samples
freshly, so captions and lyrics still vary between runs.

### Interactive mode

Run with no flags in a terminal:

```sh
acestep
acestep generate
```

You get a guided wizard for the prompt, style, lyrics, vocal mode, duration and
output format. Any flag you *do* pass skips the corresponding step.

### Scripting

`--json` writes a machine-readable result to stdout and keeps diagnostics on
stderr, so `acestep` composes with `jq` and friends:

```sh
acestep generate -p "lofi" -d 30 --json | jq -r '.tracks[]'
```

```json
{
  "tracks": ["/home/you/Music/acestep/lofi-20260925-142233.mp3"],
  "elapsed": "2m41.3s",
  "seed": -1,
  "request": { "caption": "lofi", "duration": 30, "output_format": "mp3" }
}
```

---

## Configuration

Every flag has a config-file and environment-variable equivalent. The config
file lives at `<root>/config.yaml`, where `<root>` is `$ACESTEP_HOME`, else the
platform config directory (`~/.config/acestep` on Linux).

```sh
# relocate the whole installation
export ACESTEP_HOME=/mnt/fast/acestep

# or per-invocation
acestep -H /mnt/fast/acestep doctor
```

| Setting | Flag | Environment | Default |
|---|---|---|---|
| Installation root | `-H`, `--home` | `ACESTEP_HOME` | platform config dir |
| Engine binaries | — | `ACESTEP_BIN_DIR` | `<root>/bin` |
| Model directory | — | `ACESTEP_MODELS_DIR` | `<root>/models` |
| Output directory | — | `ACESTEP_OUTPUT_DIR` | `<root>/outputs` |
| Default duration | `--duration` | `ACESTEP_GENERATE_DURATION` | `60` |
| Default format | `--format` | `ACESTEP_GENERATE_OUTPUT_FORMAT` | `mp3` |
| Model repository | `setup --models` | `ACESTEP_MODELS_REPO` | `Serveurperso/ACE-Step-1.5-GGUF` |
| `ace-lm` binary | — | `ACESTEP_BINARIES_ACE_LM` | `ace-lm` |

Example `config.yaml`:

```yaml
models:
  repo: Serveurperso/ACE-Step-1.5-GGUF
  preset: lowvram
generate:
  duration: 45
  output-format: wav24
```

---

## Command reference

```
acestep setup       Build the engine and download the models
acestep generate    Generate a track from a text prompt
acestep models      Inspect and manage downloaded models
acestep doctor      Diagnose the installation
acestep version     Print version information
```

### `acestep setup`

| Flag | Description |
|---|---|
| `--cpu` | Build the CPU backend (default) |
| `--cuda` | Build the CUDA backend for NVIDIA GPUs |
| `--vulkan` | Build the Vulkan backend for AMD/Intel GPUs |
| `--rocm` | Build the ROCm backend |
| `--all` | Build every backend and select at runtime |
| `--models <spec>` | `lowvram`, `standard`, `essential`, `quality`, a role, or a `.gguf` filename |
| `--force` | Re-download models that are already present |
| `--skip-binaries` | Download models without building the engine |
| `--binaries-only` | Build the engine without downloading models |
| `--list-presets` | Show the model bundles and exit |
| `--ref <git-ref>` | Build a specific acestep.cpp revision |

### `acestep generate`

| Flag | Default | Description |
|---|---|---|
| `-p, --prompt` | — | Text description of the song |
| `-l, --lyrics` | — | Lyrics: inline text or a path to a `.txt` file |
| `-d, --duration` | `60` | Target seconds; `0` lets the model decide |
| `-o, --output` | derived | Output path |
| `-F, --format` | `mp3` | `mp3`, `wav16`, `wav24`, `wav32` |
| `--style` | — | Extra style tag appended to the prompt |
| `--instrumental` | `false` | No vocals |
| `-n, --variations` | `1` | Number of variations |
| `--bpm` | auto | Tempo in BPM |
| `--key` | auto | Key and scale, e.g. `"C major"` |
| `--time-signature` | auto | Numerator, e.g. `4` for 4/4 |
| `--language` | auto-detect | BCP-47 code, e.g. `en`, `fr`, `ja` |
| `--seed` | random | Fixed seed for the DiT |
| `--steps` | model default | DiT iterations (8 turbo, 50 sft) |
| `--shift` | model default | Timestep shift |
| `--lm-model` | registry first | Planner LM, filename without `.gguf` |
| `--synth-model` | registry first | DiT, filename without `.gguf` |
| `--task` | `text2music` | `cover`, `repaint`, `lego`, `extract`, `complete` |
| `--src-audio` | — | Source audio for non-`text2music` tasks |
| `--ref-audio` | — | Timbre reference audio |
| `--codes` | — | Pre-computed FSQ codes; skips the LM stage |
| `--vae-chunk` | engine default | Latent frames per VAE tile; lower on low memory |
| `--vae-overlap` | engine default | VAE tile overlap frames |
| `--skip-lm` | `false` | Run only `ace-synth` |

### `acestep models`

```sh
acestep models list                 # what is on disk
acestep models presets              # available bundles and sizes
acestep models fetch lowvram        # a bundle
acestep models fetch dit            # every DiT variant
acestep models fetch acestep-v15-sft-Q8_0.gguf
acestep models remove vae-BF16.gguf
```

---

## How it works

```
acestep generate -p "lofi" -d 30
        │
        │  1. build request.json from flags
        ▼
  ┌───────────┐   lyrics + bpm + key + FSQ audio codes   ┌──────────────┐
  │  ace-lm   │ ───────────────────────────────────────► │ request0.json│
  │ (planner) │                                          └──────┬───────┘
  └───────────┘                                                 │
                                                               │  2. render
  ┌───────────┐   stereo 48 kHz WAV / MP3                       │
  │ ace-synth │ ◄───────────────────────────────────────────────┘
  │ (DiT+VAE) │ ──► request00.mp3 ──► outputs/<name>.mp3
  └───────────┘
```

The engine is a two-stage pipe driven entirely by a JSON document. `acestep`
owns everything around that: it writes the request, runs each stage with its
working directory pointed at a private workspace, parses progress out of the
log stream, and moves finished tracks into your output directory under a name
you chose.

Model selection is part of the request, not the command line — both stages read
`lm_model` and `synth_model` from the JSON and resolve them by scanning the
models directory. This is documented in
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), which also describes the full
`AceRequest` schema.

---

## Troubleshooting

Start here:

```sh
acestep doctor
```

It checks the directories, the engine binaries, the presence of each model
role, free disk space and system memory, and tells you the fix for anything
missing.

| Symptom | Cause | Fix |
|---|---|---|
| `engine binary not found` | `acestep setup` never completed | `acestep setup --binaries-only` |
| `failed to load LM` | wrong or missing planner LM | `acestep models fetch lm`, or set `--lm-model` |
| Out of memory (CUDA) | VRAM too small for the DiT | `--models lowvram`, or build with `--cpu` |
| Out of memory (CPU) | system RAM too small | `--models lowvram`, `--vae-chunk 256` |
| Killed with no message | the OOM killer reaped the engine | lower the bundle; check `dmesg` |
| `ace-synth produced no audio` | model name in the request not on disk | `acestep models list` |
| Very slow | GPU thrashing VRAM | see [docs/HARDWARE_GUIDE.md](docs/HARDWARE_GUIDE.md) |
| Generation hangs | stale engine process from a previous run | `pkill -f ace-lm` |

Get the engine's own log with:

```sh
acestep generate -p "lofi" -v
```

`Ctrl+C` is always safe: the whole process group is signalled and cleaned up.

---

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for the
workflow, and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for the ground rules.

```sh
make check        # gofmt, go vet, go test
make race         # tests with the race detector
make cross        # cross-compile every release platform
```

## License

MIT — see [LICENSE](LICENSE).

## Credits

- [acestep.cpp](https://github.com/ace-step/acestep.cpp) — the C++17/GGML
  engine this CLI supervises.
- [ACE-Step 1.5](https://github.com/ace-step/ACE-Step-1.5) by ACE Studio and
  StepFun — the model.
- [GGUF](https://github.com/ggml-org/ggml) and llama.cpp, on which the engine's
  runtime is built.
- [Serveurperso/ACE-Step-1.5-GGUF](https://huggingface.co/Serveurperso/ACE-Step-1.5-GGUF)
  — the pre-quantized weights.
- [Cobra](https://github.com/spf13/cobra), [Viper](https://github.com/spf13/viper),
  [Bubbletea](https://github.com/charmbracelet/bubbletea) and
  [progressbar](https://github.com/schollz/progressbar) — the CLI and TUI stack.

This project is an independent front end and is not affiliated with or endorsed
by the ACE-Step or acestep.cpp authors.
