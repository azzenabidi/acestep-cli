# Hardware guide

The engine loads four models before it can render a single sample. On a
low-memory machine the difference between a working setup and an OOM kill is
almost always *which* quantization you chose and *where* you put the compute.

This guide is the short version. Run `acestep doctor` first — it tells you what
is actually installed and what is missing.

---

## 1. How much memory do I need?

The four models, at the quantizations used by each bundle:

| Role | Model | Q8_0 | Q5_K_M | Q4_K_M |
|---|---|---|---|---|
| Text encoder | `Qwen3-Embedding-0.6B` | 748 MB | — | — |
| Planner LM | `acestep-5Hz-lm-0.6B` | — | 640 MB | — |
| Planner LM | `acestep-5Hz-lm-1.7B` | — | 1.6 GB | — |
| Planner LM | `acestep-5Hz-lm-4B` | 4.2 GB | — | — |
| DiT | `acestep-v15-turbo` (2B) | 2.4 GB | 1.9 GB | 1.5 GB |
| DiT | `acestep-v15-sft` (2B) | 3.4 GB | — | — |
| DiT | `acestep-v15-xl-*` (4B) | ~9.5 GB | — | — |
| VAE | `vae-BF16` | 322 MB | — | — |

The VAE is always BF16. It is small, bandwidth-bound, and quality-critical;
do not try to quantize it to save 200 MB.

**A bundle must contain exactly one model per role.** `acestep models presets`
lists them.

### Working out your budget

Add the four rows for your chosen bundle, then add headroom:

- **CPU-only:** total system RAM must exceed the sum by ~1.5×. KV cache for the
  LM, activation buffers and the tiled VAE decoder all need room.
- **GPU:** VRAM must hold the whole working set for a fast run, or the engine
  must be able to offload cleanly to system RAM. Partial residency with no
  offload path is the single most common cause of an out-of-memory failure.

| Bundle | Weights | Minimum RAM | Comfortable RAM | Ideal VRAM |
|---|---|---|---|---|
| `lowvram` | ~3.4 GB | 4 GB | 8 GB | none, or 2 GB offload |
| `standard` | ~4.5 GB | 6 GB | 12 GB | 6 GB |
| `essential` | ~7.7 GB | 8 GB | 16 GB | 8 GB |
| `quality` | ~12 GB | 16 GB | 24 GB | 12 GB |

---

## 2. Choosing a bundle

```sh
acestep models presets              # see sizes
acestep setup --models lowvram      # small machines
acestep setup --models standard     # 8 GB VRAM
acestep setup --models essential    # 8+ GB VRAM, upstream default
acestep setup --models quality      # 12+ GB VRAM, 50-step SFT DiT
```

`acestep models fetch lm` grabs every LM variant so you can switch between them
with `--lm-model` without re-downloading.

Mixing bundles by hand works too:

```sh
acestep models fetch text-encoder,vae
acestep models fetch acestep-5Hz-lm-0.6B-Q5_K_M.gguf
acestep models fetch acestep-v15-turbo-Q4_K_M.gguf
```

---

## 3. Build the engine for your GPU

| Hardware | Flag | Notes |
|---|---|---|
| NVIDIA (Compute Capability 6.0+) | `--cuda` | Needs CUDA 12.x |
| AMD, Intel (via Vulkan) | `--vulkan` | Most reliable on older iGPUs |
| AMD (ROCm) | `--rocm` | Linux only |
| Apple Silicon | *(none)* | Metal and Accelerate are auto-enabled |
| No GPU, or a very small one | `--cpu` | **See the MX230 section below** |
| Unsure | `--all` | Builds every backend, picks at runtime |

```sh
acestep setup --cuda
acestep setup --all          # larger binary, no rebuild needed later
```

Compiling every backend takes noticeably longer and produces a bigger binary.
If you know your hardware, name it.

### The MX230, and other 2 GB cards

A 2 GB card cannot hold a useful slice of the 2.4 GB turbo DiT, let alone the
planner LM alongside it. Forcing the model onto it causes VRAM thrashing that is
dramatically *slower* than a clean CPU run — and frequently an out-of-memory
failure. Do this instead:

```sh
# 1. Build without a GPU backend at all
acestep setup --cpu --models lowvram

# 2. Generate, keeping the VAE decoder in small tiles
acestep generate -p "lofi beats" -d 30 --vae-chunk 256 --vae-overlap 32
```

Expect minutes rather than seconds. A 30-second sketch at 8 DiT steps on a
modern laptop CPU is roughly 2–5 minutes.

If you would rather use the GPU for the LM and keep the DiT on the CPU, build
with `--vulkan` and let the engine's own backend selection decide; that path
handles offload better than trying to force it from the command line.

### Apple Silicon

Nothing to do. Metal and Accelerate BLAS are enabled by default. Use the
`standard` bundle on 16 GB of unified memory and `essential` on 32 GB+.

---

## 4. Tuning generation for slower machines

| Flag | Effect | When to use it |
|---|---|---|
| `--vae-chunk 256` | Fewer latent frames per VAE tile | System RAM pressure, CPU mode |
| `--vae-overlap 32` | Smaller tile overlap | As above, with `--vae-chunk` |
| `--steps 8` | Turbo's native step count | Already the default for turbo |
| `--variations 1` | One song at a time | Keeps peak memory at one track's worth |
| `-d 30` | Shorter tracks | Latent length scales with duration |

The DiT holds latents for the whole track in memory, so **duration is the
biggest lever on peak memory**. Halving `-d` more than halves the DiT
allocation.

`--vae-chunk` only affects the decoder, so it is the right knob when the
failure happens *after* the DiT has finished, and the wrong one when the DiT
itself cannot load.

---

## 5. Reading failure messages

| Message | Meaning | Fix |
|---|---|---|
| `ggml_backend_cuda_init: out of memory` | Model does not fit in VRAM | Smaller bundle, or `--cpu` |
| `std::bad_alloc` | Host allocation failed | More system RAM, or a smaller bundle |
| Process killed with no output | The OOM killer reaped it | `dmesg -T \| tail`; shrink the bundle |
| `failed to load LM` | `lm_model` names a file that is not on disk | `acestep models list` |
| `no such file: ...gguf` | The models directory is wrong | `acestep -H <root> doctor` |
| Hangs at 0% | Deadlock in a multi-GPU/offload path | Rebuild with `--cpu` to isolate |
| Very slow, no GPU utilisation | The engine fell back to CPU | Check `acestep setup --vulkan`/`--cuda` build flags |

---

## 6. Verifying an installation

```sh
acestep doctor
```

It reports, with a fix for each:

- every runtime directory exists
- `ace-lm` and `ace-synth` are present and executable
- at least one model per role is on disk
- free disk space on the models volume
- total system RAM

For a definitive answer on the compute backend, watch the engine's own log:

```sh
acestep generate -p "test" -d 5 -v 2>&1 | head -40
```

The first lines name the backend ggml selected.

---

## 7. Reducing disk usage

`essential` is ~7.7 GB. To go smaller:

```sh
acestep models remove acestep-5Hz-lm-4B-Q8_0.gguf   # drop the 4B LM
acestep models remove acestep-v15-sft-Q8_0.gguf     # drop the SFT DiT
```

`acestep models list` shows the size of everything currently on disk.

---

## 8. Reporting a hardware problem

Open an issue with the output of `acestep doctor`, the hardware line from
`acestep version`, and the full engine log from a `-v` run. The upstream
[acestep.cpp](https://github.com/ace-step/acestep.cpp/issues) tracker is the
right place if the fault is inside the engine rather than this CLI.
