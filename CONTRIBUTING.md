# Contributing to acestep

Thanks for your interest. This is a small project and reviews usually happen
within a few days.

`acestep` is a front end for [acestep.cpp](https://github.com/ace-step/acestep.cpp).
If your change concerns the model's behaviour, the audio quality, or the engine
itself, it belongs upstream. This repository owns the CLI: downloading,
configuration, request construction, process supervision and the terminal UI.

---

## Before you start

**Open an issue first** for anything beyond a small fix. It is cheaper to
discover that a feature conflicts with something in progress than after you
have written it.

Especially worth discussing first:

- new engine fields or flags
- changes to on-disk paths or `config.yaml`
- anything that changes the default output location
- new dependencies

---

## Getting set up

You need Go 1.25+ and a C++17 toolchain. The toolchain is only needed to
*build the engine*; you can develop and test the CLI without it, because the
engine tests use a fake binary.

```sh
git clone https://github.com/azzenabidi/acestep-cli.git
cd acestep-cli
make check          # gofmt, go vet, go test
make build          # binary in bin/
./bin/acestep doctor
```

There is a `.mise.toml` pinning Go 1.27.1 if you use mise.

---

## Making a change

1. Branch from `main`.
2. Make the change, with tests.
3. `make check`.
4. Open a pull request against `main`.

### What we look for

- **Correctness against the engine contract.** The single most important thing.
  `pkg/request` mirrors a C++ parser exactly; a renamed JSON tag is a silent
  runtime break. See `docs/ARCHITECTURE.md`.
- **Tests.** New behaviour needs a test that fails without your change.
  The engine tests are the model to follow — see §"Testing" below.
- **Comments only where they earn their place.** Explain *why*, especially
  around the pointer fields in `pkg/request` and the process-group handling in
  `pkg/engine`. Do not narrate what the next line obviously does.
- **No new dependencies** without discussion. The dependency list is short on
  purpose: this ships as a single static binary.
- **Cross-platform.** Anything touching `os/exec`, signals or paths must work
  on Windows. `pkg/engine/process_windows.go` exists for this reason.

### House style

- Idiomatic Go, `gofmt`-clean. No clever abstractions; this is a CLI that people
  need to read.
- Errors are wrapped with context: `fmt.Errorf("build engine: %w", err)`.
- User-facing output goes through the helpers in `cmd/ui.go` so that colour,
  `--json` and quiet mode stay consistent. Do not call `fmt.Println` in a
  command.
- Flags get a `Default:` line in their help text. It is user documentation.

---

## Testing

```sh
make test          # go test ./...
make race          # go test -race ./...
make cover         # coverage summary
```

The engine tests re-execute the test binary as a stand-in for `ace-lm` and
`ace-synth`. The fake is selected by `ACESTEP_FAKE_ENGINE` and installed under
the real binary names, so `pkg/engine` is tested against the actual command
interface without a model or a C++ compiler.

When you change something the engine does, extend the fake to reproduce the new
behaviour. Keep it minimal, but reproduce the parts the supervisor depends on:
output naming, numeric ordering, both output streams, exit codes.

Please include `-race` in any run that touches concurrency.

---

## Commit and PR style

Short, imperative subject lines. One logical change per commit. Reference the
issue in the PR body, not the subject.

```
fix: terminate the engine process group on cancellation

SIGTERM alone left the DiT worker running, holding 4 GB of VRAM
after Ctrl+C. Escalate to SIGKILL after the grace period.
```

---

## Reporting bugs

Use the bug report template. **Run `acestep doctor` and paste the output** —
it answers most environment questions immediately. Include the exact command, a
`-v` run's output, and your hardware. See
[docs/HARDWARE_GUIDE.md](docs/HARDWARE_GUIDE.md) first if generation failed
rather than crashed.

---

## Security

Do not include model weights, API keys or Hugging Face tokens in a commit. If
`acestep setup` ever needs a token for a private repository, read it from the
environment and document that clearly. Report security issues privately to the
maintainer rather than opening a public issue.

---

## Code of conduct

Participation is governed by [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). By
contributing you agree to uphold it.

## License

Contributions are accepted under the [MIT License](LICENSE).
