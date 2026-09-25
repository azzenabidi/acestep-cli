# Architecture

How `acestep` is put together, and why. For the engine's own documentation see
[acestep.cpp/docs/ARCHITECTURE.md](https://github.com/ace-step/acestep.cpp/blob/master/docs/ARCHITECTURE.md).

---

## 1. The constraint that shapes everything

`acestep.cpp` is a two-stage pipe driven **entirely by a JSON document**. There
is no command-line flag for a prompt, a duration or a model choice:

```bash
ace-lm    --models models --request request.json   # -> request0.json
ace-synth --models models --request request0.json  # -> request00.mp3
```

Three consequences follow, and they dictate the design:

1. **Model selection lives in the request, not the command line.** `lm_model`
   and `synth_model` are fields in the JSON, resolved by scanning `--models`
   for GGUF filenames (without the `.gguf` suffix). An empty value falls to the
   first match in the registry.
2. **The engine writes into its working directory.** Neither binary takes an
   output path. `request0.json` and `request00.mp3` land wherever the process
   was started.
3. **The input request is never modified.** Outputs are always numbered.

So this CLI is a request builder, a working-directory manager, a log parser and
a process supervisor. It deliberately contains no music-generation logic of its
own.

---

## 2. Layout

```
cmd/                     CLI surface (Cobra), one file per subcommand
  root.go                flags, layout resolution, signal handling
  setup.go               clone + CMake build + model download
  generate.go            request construction, engine invocation
  models.go              model inventory
  version.go             version + doctor
  ui.go                  terminal output helpers
  platform_unix.go       statfs, /proc/meminfo
  platform_other.go      fallbacks for Windows and friends
pkg/
  request/               the AceRequest schema, and nothing else
  downloader/            resumable Hugging Face GGUF fetcher
  engine/                subprocess supervisor
  tui/                   Bubbletea wizard and progress view
docs/                    this file and the hardware guide
scripts/install.sh       curl | sh installer
```

`pkg/request` has no dependencies beyond the standard library. It is the
contract with the engine, and it is kept isolated so a schema change is a
single-file review.

---

## 3. `pkg/request` — the engine contract

The `Request` struct mirrors the engine's `AceRequest` field-for-field, with
JSON tags spelled exactly as the C++ parser expects them. Renaming a tag is a
silent runtime break, so the spelling is pinned by
`TestJSONFieldNamesMatchEngineContract`.

**Every optional field is a pointer.** The engine's rule is that an omitted
field is equivalent to its default. That makes `nil` the natural encoding of
"unset", and `omitempty` on a pointer omits exactly the nil fields.

This is not stylistic. Several fields have *non-zero* defaults:

| Field | Default | Consequence of using a plain `int`/`float64` |
|---|---|---|
| `seed` | `-1` | A user's explicit `--seed 0` would be dropped and become random |
| `latent_rescale` | `1.0` | An explicit `0.0` would become `1.0` |
| `peak_clip` | `10` | An explicit `0` (disable clipping) would become `10` |
| `mp3_bitrate` | `128` | An explicit `320` would work, but `0` would not |
| `adapter_scale` | `1.0` | Same as `latent_rescale` |
| `audio_cover_strength` | `1.0` | An explicit `0.0` would become `1.0` |

With a plain value plus `omitempty`, any of those zeroes is silently swallowed.
`TestExplicitZeroIsTransmitted` exists to stop that regression coming back.

`Validate` enforces the invariants the engine relies on: a non-empty caption,
a duration in `[0, 600]`, known enum values, positive batch sizes, and a source
for the tasks that need one. It runs before the request is written, so a
mistake is a clean error message rather than a non-zero exit from a
multi-gigabyte model load.

### Lyrics are the only switch for vocals

There is no `instrumental` flag in the engine. The `lyrics` field is the single
source of truth:

| Value | Meaning |
|---|---|
| `""` | The LM writes lyrics from the caption |
| `"[Instrumental]"` | No vocals; the DiT's training sentinel, passed through verbatim |
| anything else | Your lyrics, used as-is |

`--instrumental` is a CLI convenience that sets the sentinel. It rejects
`--lyrics` in the same invocation rather than picking a winner silently.

---

## 4. `pkg/engine` — the supervisor

`Pipeline.Generate` runs one request through both stages.

### Working directory isolation

Each run gets a fresh `job-*` directory under the work directory via
`os.MkdirTemp`. The request is written there, and each engine stage runs with
`cmd.Dir` pointing at it, because the engine writes its output relative to the
process working directory. Afterwards the collector moves finished audio into
the output directory and `defer os.RemoveAll` removes the job directory.

`TestGenerateCleansUpWorkspace` asserts the work directory is empty afterwards.

### Output naming

The engine names files by variation index, not by intent:

```
request{I}{B}.{ext}
        │    └── DiT variation B, from synth_batch_size
        └─────── LM variation I, from lm_batch_size
```

`lm_batch_size = 3, synth_batch_size = 1` produces `request00.mp3`,
`request10.mp3`, `request20.mp3` — note the **leading** zero on the second
field. The collector sorts those numerically rather than lexicographically, so
`request10` does not sort between `request1` and `request2`, then renames them
to `name.mp3` (single track) or `name-01.mp3` … `name-04.mp3` (multiple).

The output extension comes from the engine's `output_format`, not from the
`--output` path, so `--output take` and `--output take.wav` behave identically
and the extension is always right.

### Process groups and cancellation

This is the part that is easy to get wrong.

`exec.CommandContext` kills only the direct child on cancellation. The engine
is a single process today, but its build spawns helper processes, and GGML
backends start worker processes of their own. Signalling only the parent leaves
those running, still holding VRAM and system RAM, with nobody to reap them.

So each stage is started in its own process group
(`Setpgid: true` on Unix, `CREATE_NEW_PROCESS_GROUP` on Windows) and shutdown is
explicit:

```
ctx cancelled  →  SIGTERM the process group
                →  wait GracePeriod (5 s)
                →  SIGKILL the process group
```

On Windows, `taskkill /T /F` is used, because `os.Process.Signal` supports only
`Kill` there and `/T` is the only reliable way to take down a tree.

`TestGenerateCancellationIsPrompt` starts a deliberately hanging stage,
cancels, and fails if `Generate` has not returned within 15 seconds.

### Log streaming and progress

Both stdout and stderr are consumed by `bufio.Scanner` on separate goroutines,
so a chatty stderr cannot deadlock against a full stdout pipe. Each line is
appended to a bounded 40-line tail (used to build the error message) and
forwarded to the `OnProgress` callback.

The C++ binaries have no machine-readable progress channel, so
`pkg/engine/progress.go` parses log lines with three permissive patterns:

| Pattern | Example |
|---|---|
| `(\d{1,3}(\.\d+)?)\s*%` | `42%`, `12.5 %` |
| `(step\|iter\|chunk\|sample)\s*(\d+)\s*(of\|/)\s*(\d+)` | `step 4/8` |
| `([a-z_][a-z_ -]{1,23}?)\s*[:=]\s*\d+%` | `sampling: 33%` |

A percentage and an `n/m` counter are alternative encodings of the same value,
so when both are present the counters are dropped rather than left to
disagree with each other. A line that matches nothing still reaches the user
verbatim via `--verbose`. Progress reporting is best-effort by design: it must
never be able to suppress a log line the user needs to diagnose a failure.

### Failure reporting

`ErrEngine` carries the stage, the exit code and the captured log tail, and
`engineError` in `cmd/` turns it into advice specific to the stage — a DiT
failure suggests checking the VAE, an LM failure suggests checking the text
encoder.

---

## 5. `pkg/downloader` — weights

Files come from `Serveurperso/ACE-Step-1.5-GGUF` on Hugging Face.

**Resumable, never half-published.** Bytes go to `<name>.gguf.part` and are
renamed into place only after the transfer completes and the byte count matches
the server's `Content-Length`. A truncated GGUF is worse than a missing one: it
loads as a corrupt model with a confusing error. An interrupted download is
resumed from the `.part` file with a `Range` request; if the server answers
`200` instead of `206` it ignored the range, and the partial file is discarded
and restarted rather than appended to.

**Presets and roles.** `--models` accepts a bundle name, a comma-separated
union, a role (`lm`, `dit`, `vae`, `text-encoder`), or an exact `.gguf`
filename. Every preset is verified by test to contain exactly one model per
role.

**Mirrors.** `BaseURL` is a field rather than a hardcoded constant, so a mirror
such as `https://hf-mirror.com` works and the download logic is testable
against an `httptest` server.

---

## 6. `pkg/tui` — the terminal UI

Two Bubbletea programs, both of which have to survive not being a terminal.

- **The wizard** (`generate` with no flags) walks through prompt, style, lyrics,
  vocal mode, duration and format. Text steps use a `textinput` field, vocal
  mode and format use arrow-key pickers.
- **The progress view** shows one bar per stage. It receives events on a
  buffered channel; progress events are dropped when the view falls behind, and
  terminal events are never dropped.

Both fall back to plain line-oriented output when stdin or stdout is not a TTY,
when `--no-ui` is passed, or when `--json` is set, so piping into `jq` produces
clean output. In that mode the progress reporter only prints a line when the
percentage actually changes, instead of emitting a carriage-return rewrite per
update.

---

## 7. Configuration

Three layers, in increasing precedence: built-in defaults, then
`config.yaml` and `ACESTEP_*` environment variables, then command-line flags.
Viper handles the merge; `pkg/config` owns the path resolution and the defaults.

The root directory is `--home`, else `$ACESTEP_HOME`, else the platform config
directory. `acestep -H` therefore relocates the entire installation, which is
what you want on a machine with a separate fast volume.

The output directory deliberately defaults to `~/Music/acestep` rather than
inside the config directory: audio in a dot-directory is easy to overlook and
easy to lose on cleanup.

---

## 8. Testing strategy

| Package | Approach |
|---|---|
| `request` | Schema pinning, pointer semantics, validation, round-tripping |
| `engine` | The test binary re-executes itself as a stand-in `ace-lm`/`ace-synth` |
| `downloader` | `httptest` servers, including one that deliberately ignores `Range` |
| `config` | Path resolution, layering, idempotency |
| `cmd` | Pure helpers: slugifying, lyrics-vs-path disambiguation, CMake flags |

The engine tests are the important ones. `TestMain` dispatches on
`ACESTEP_FAKE_ENGINE`, and the fake is installed as `ace-lm` and `ace-synth` by
copying the test binary to those names. The fakes reproduce the parts of the
contract that matter — numbered outputs, the `request0.json` →
`request00.mp3` naming scheme, progress on both streams, non-zero exits — so
the supervisor is tested against the real interface without needing a
multi-gigabyte model or a C++ toolchain.

Everything passes under `-race`, including the cancellation path.
