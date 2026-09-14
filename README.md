# agentconv

Move project instructions between Claude Code, GitHub Copilot CLI, and OpenAI Codex without losing track of which files people should edit.

## Install

macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/chrisle/agentconv/main/scripts/install.sh | sh
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/chrisle/agentconv/main/scripts/install.ps1 | iex
```

The installer downloads the matching latest-release binary: macOS arm64/x86_64, Linux arm64/x86_64, or Windows x86_64.

## Use it

Start by seeing what the repository already contains:

```sh
agentconv detect
```

For most projects, use `sync`. It keeps one convention as the source of truth and creates only the compatibility files required by another tool:

```sh
# Keep Claude files authoritative; make the project work in Codex too.
agentconv sync --from claude --to codex

# Keep Claude files authoritative; make the project work in Copilot too.
agentconv sync --from claude --to copilot
```

Generated `AGENTS.md` files contain the full source instructions and an ownership comment. Edit the original Claude file, not the generated copy, then run `sync` again.

Use `convert` when you want a full translated target layout instead:

```sh
agentconv convert --from claude --to copilot
agentconv convert --from claude --to codex
agentconv convert --from copilot --to claude
```

Pass a project directory after the command if it is not the current directory:

```sh
agentconv sync --from claude --to codex ../my-project
```

## Before files change

Agentconv shows its planned writes, possible removals, and any conversion limitations, then asks for confirmation. Enter `y` or `yes` to continue.

```sh
agentconv sync --from claude --to codex --dry-run
agentconv sync --from claude --to codex --force
```

`--dry-run` changes nothing. `--force` skips confirmation and permits replacement of protected files, so use it only when you have reviewed the plan.

Agentconv records generated files in `.agentconv.json`. It refreshes only its own unedited output and preserves hand-edited files unless `--force` is supplied. Remove generated files with:

```sh
agentconv clean
```

## Version and updates

Check the installed version:

```sh
agentconv --version
```

Update to the matching latest GitHub Release binary:

```sh
agentconv update
```

The command shows the executable it will replace and asks for confirmation. Use `agentconv update --dry-run` to inspect the download, or `agentconv update --force` to update without the prompt.

## What gets converted

| Source | Target | Result |
|---|---|---|
| Claude `CLAUDE.md` | Codex/Copilot `AGENTS.md` | Full generated copy with a source-of-truth marker in `sync`; plain translated target file in `convert`. |
| Claude skills | Codex `.agents/skills/` | Skill directories are copied. |
| Claude commands | Codex `.agents/skills/` | Commands become skills. |
| Claude rules | Copilot `.github/instructions/` | Path rules are translated. |
| Claude agents | Copilot `.github/agents/` | Core name, description, tool, model, and effort fields are translated. |
| Copilot instructions, agents, skills, prompts, and MCP config | Claude layout | Converted by `convert --from copilot --to claude`. |

Some concepts do not have a safe portable Codex file equivalent. Codex sync reports Claude path rules, subagents, settings, hooks, and MCP configuration rather than silently inventing a target configuration.

## Developer guide

### Build and test

This is a Go 1.25+ project.

```sh
make build
make test
```

Build local release artifacts with:

```sh
make release
```

### Releases

Pushing a `v*` tag runs [.github/workflows/release.yml](.github/workflows/release.yml). It uses the Arc self-hosted pools to test and build:

- macOS arm64 and x86_64
- Linux arm64 and x86_64
- Windows x86_64

The publish job attaches all artifacts to the matching GitHub Release. A workflow dispatch can publish a specified tag as well.

Release artifacts embed the tag version (without the leading `v`) through Go linker flags. For a local versioned build, use `make build VERSION=0.4.0`.

The macOS/Linux and PowerShell installers live in [scripts](scripts/) and download release assets from `chrisle/agentconv`.

### Layout

- `cmd/agentconv/` — CLI implementation and Go tests
- `.github/workflows/release.yml` — Arc release pipeline
- `scripts/` — release installers

The project uses `gopkg.in/yaml.v3` for Markdown frontmatter and keeps generated-file ownership in `.agentconv.json`.
