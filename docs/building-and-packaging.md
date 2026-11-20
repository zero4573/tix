# Building and packaging

tix builds with Nix (`flake.nix`, `nix/`) or with `configure` and `make`. The checks to run
are in `AGENTS.md`; every pin is in `UPDATES.md`.

## configure and make

`./configure` takes `--prefix` (default `/usr/local`), `--bindir` (`PREFIX/bin`), `--datadir`
(`PREFIX/share`) and `GO=go`, and writes `config.mk`. It needs Go to build; git, podman or
docker, and Claude Code are only needed to run, so missing ones are warnings.

| Target | Does |
|---|---|
| `all` | `tix`, stamped with `git describe` |
| `test`, `check` | the tests; `check` adds `go vet` and `gofmt` |
| `bench` | search cost and latency; with the default stub embedder a regression guard, not real-model numbers |
| `eval` | the plugin's behavioural evals on a real model, on your account (about $1-3 per case run) |
| `install`, `uninstall` | `tix` and its zsh, bash and fish completions; `DESTDIR` is honoured |
| `cross` | linux, darwin and windows on amd64 and arm64 into `dist/` |

`tix` is a file target (its sources are its prerequisites), so `sudo make install` after
`make` doesn't rebuild as root. `make check TESTLOG=<file>` runs the tests once with `-json`
into the file and prints their plain output through `jq`; it refuses to start without `jq`,
since the pipe would otherwise die of SIGPIPE and leave the log empty.

Run one eval case with `make eval EVAL_ARGS="--case review-proof --runs 1"`;
`TIX_EVAL_MAX_USD` caps the cost.

## Scaffold history

`go generate ./assets` merges the hashes of every shipped scaffold version into
`assets/scaffold-history.json` and never drops a hash or path, so a rewritten or partial
history loses nothing. It reads git, so it needs the full history; `historicRoots` lists the
folders the scaffold lived in before (`tools/vault-scaffold/`).

## CI and releases

CI (`.github/workflows/ci.yml`) runs by hand only (Actions, CI, Run workflow) until tix is
released; to run it on every change, add `push: branches: [main]` and `pull_request:` under
`on:`. The CLI tests (sh stubs, real git) skip themselves on the Windows runner, so there
it runs vet and the unit tests. `release.yml` is a placeholder: set the repository variable `RELEASES_ENABLED=true`,
make it run on `v*` tags, and build the archives (tix and completions per OS and arch) with
checksums, for example with goreleaser and `gh release create "$GITHUB_REF_NAME" dist/*`.

## Nix

- `nix/tools.nix` builds tix. The extra library folders the embedder searches after
  `$TIX_LIB_PATH` (libstdc++, libgomp) are set with `-ldflags -X` on `embed.DefaultLibPath`:
  the prebuilt llama.cpp library needs both and NixOS keeps them out of the default search
  path. `llamaRuntimeLibs` links both into one folder, where `go test` and `make bench` find
  them through `embed.RuntimeLibDir` when `TIX_LIB_PATH` is unset. The source fileset takes
  `docs/`, `README.md` and `AGENTS.md` because the assets tests check the files the skills
  refer to.
  The scanners `tix ws sec-check` runs use your `go` (the repo's toolchain) and `npm` first.
- `nix/home.nix` builds the environment defaults into the commands, so they apply right after a
  switch in every shell, terminal or service; an exported variable still wins.
- `nix/obsidian.nix`'s NixOS module installs the Flathub Obsidian and revokes what its
  manifest (flathub/md.obsidian.Obsidian) grants beyond the vault folder: `--filesystem=home`,
  `/mnt`, `/run/media`, `/media`, a Discord-RPC socket folder, read-only gnupg and fonts, and a
  persisted `~/.ssh`.
- `nix/obsidian.nix`, per vault: the Tasks plugin is seeded once (files only if missing,
  `community-plugins.json` only if absent; Obsidian's updater owns it afterwards; the pin is
  `assets/obsidian/tasks-plugin.json`, shared with `tix vault init`). Its settings are enforced
  from `assets/obsidian/tasks-settings.json` on every switch, so changes made in Obsidian are
  reset unless copied back from the vault's
  `.obsidian/plugins/obsidian-tasks-plugin/data.json`. The Minimal theme is seeded once and
  selected only when the vault has no theme; readable line length is turned off once, only if
  unset. Document-portal vault paths are rewritten to the real path, keeping the vault id, and
  unregistered vaults are added: a vault picked in the file chooser can be registered in
  `obsidian.json` as `/run/user/<uid>/doc/<id>/<vault>`, and that FUSE mount gets no inotify
  events for edits made outside it (agents, other editors), so Obsidian would never see them.
  This step is skipped while Obsidian runs, since it rewrites `obsidian.json` on exit; the next
  switch catches up.
