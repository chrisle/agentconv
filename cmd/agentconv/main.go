// agentconv converts repository agent configuration between Claude Code,
// GitHub Copilot CLI, and OpenAI Codex conventions.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const releaseRepository = "chrisle/agentconv"

// version is set for release builds with -ldflags "-X main.version=<version>".
var version = "dev"

const manifestName = ".agentconv.json"

type fileOp struct {
	Path              string
	Content           []byte
	Source, Direction string
	Executable        bool
}
type plan struct {
	Direction       string
	Covers          map[string]bool
	Ops             []fileOp
	Warnings, Notes []string
	targets         map[string]bool
}

func newPlan(direction string) *plan {
	return &plan{Direction: direction, Covers: map[string]bool{direction: true}, targets: map[string]bool{}}
}
func (p *plan) add(op fileOp) {
	if p.targets[op.Path] {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%s: produced twice (from %s); keeping the first", op.Path, op.Source))
		return
	}
	p.targets[op.Path] = true
	if op.Direction == "" {
		op.Direction = p.Direction
	}
	p.Ops = append(p.Ops, op)
}
func (p *plan) warn(s string) { p.Warnings = append(p.Warnings, s) }

type generatedRecord struct {
	Source    string `json:"source"`
	SHA       string `json:"sha256"`
	Direction string `json:"direction"`
}
type manifest struct {
	Version   int                        `json:"version"`
	Generated map[string]generatedRecord `json:"generated"`
}

func loadManifest(root string) manifest {
	var m manifest
	b, e := os.ReadFile(filepath.Join(root, manifestName))
	if e != nil || json.Unmarshal(b, &m) != nil || m.Generated == nil {
		return manifest{Version: 1, Generated: map[string]generatedRecord{}}
	}
	return m
}
func saveManifest(root string, m manifest) error {
	p := filepath.Join(root, manifestName)
	if len(m.Generated) == 0 {
		_ = os.Remove(p)
		return nil
	}
	b, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(p, append(b, '\n'), 0644)
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type result struct{ Written, Unchanged, Skipped, Modified, Removed, Stale []string }

func apply(p *plan, root string, force, dry bool) (result, error) {
	m := loadManifest(root)
	var r result
	for _, op := range p.Ops {
		target := filepath.Join(root, filepath.FromSlash(op.Path))
		d := digest(op.Content)
		rec, known := m.Generated[op.Path]
		old, e := os.ReadFile(target)
		if e == nil {
			if string(old) == string(op.Content) {
				r.Unchanged = append(r.Unchanged, op.Path)
				m.Generated[op.Path] = generatedRecord{op.Source, d, op.Direction}
				continue
			}
			if !force && !known {
				r.Skipped = append(r.Skipped, op.Path)
				continue
			}
			if !force && known && rec.SHA != digest(old) {
				r.Modified = append(r.Modified, op.Path)
				continue
			}
		}
		r.Written = append(r.Written, op.Path)
		if dry {
			continue
		}
		if e = os.MkdirAll(filepath.Dir(target), 0755); e != nil {
			return r, e
		}
		if e = os.WriteFile(target, op.Content, 0644); e != nil {
			return r, e
		}
		if op.Executable {
			_ = os.Chmod(target, 0755)
		}
		m.Generated[op.Path] = generatedRecord{op.Source, d, op.Direction}
	}
	for path, rec := range m.Generated {
		if p.targets[path] || !p.Covers[rec.Direction] {
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(path))
		b, e := os.ReadFile(target)
		if e == nil && digest(b) != rec.SHA {
			r.Stale = append(r.Stale, path)
			continue
		}
		r.Removed = append(r.Removed, path)
		if !dry {
			_ = os.Remove(target)
			delete(m.Generated, path)
			prune(filepath.Dir(target), root)
		}
	}
	for _, x := range [][]string{r.Written, r.Unchanged, r.Skipped, r.Modified, r.Removed, r.Stale} {
		sort.Strings(x)
	}
	if !dry {
		return r, saveManifest(root, m)
	}
	return r, nil
}
func prune(dir, root string) {
	for dir != root {
		es, e := os.ReadDir(dir)
		if e != nil || len(es) > 0 {
			return
		}
		_ = os.Remove(dir)
		dir = filepath.Dir(dir)
	}
}

type claudeLayout struct {
	Memory, Rules, Agents, Skills, Commands []string
	Settings, MCP                           string
}
type copilotLayout struct {
	AgentsMD, Instructions, Agents, Skills, Prompts, Hooks []string
	RepoInstructions, Settings, MCP                        string
}

var ignored = map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true, "dist": true, "build": true, "target": true, "__pycache__": true, ".pytest_cache": true, ".idea": true, ".vscode": true}

func rel(root, path string) string {
	x, e := filepath.Rel(root, path)
	if e != nil {
		return path
	}
	return filepath.ToSlash(x)
}
func walkNamed(root, name string, exclude map[string]bool) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return nil
		}
		if d.IsDir() {
			if ignored[d.Name()] || exclude[rel(root, path)] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == name {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}
func globFiles(base, suffix string) []string {
	var out []string
	_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, e error) error {
		if e == nil && !d.IsDir() && strings.HasSuffix(d.Name(), suffix) {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}
func skillsIn(base string) []string {
	es, e := os.ReadDir(base)
	if e != nil {
		return nil
	}
	var out []string
	for _, x := range es {
		p := filepath.Join(base, x.Name(), "SKILL.md")
		if x.IsDir() {
			if _, e = os.Stat(p); e == nil {
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}
func detectClaude(root string) claudeLayout {
	c := filepath.Join(root, ".claude")
	l := claudeLayout{Memory: walkNamed(root, "CLAUDE.md", map[string]bool{".claude": true}), Rules: globFiles(filepath.Join(c, "rules"), ".md"), Agents: globFiles(filepath.Join(c, "agents"), ".md"), Skills: skillsIn(filepath.Join(c, "skills")), Commands: globFiles(filepath.Join(c, "commands"), ".md")}
	if p := filepath.Join(c, "CLAUDE.md"); exists(p) && !exists(filepath.Join(root, "CLAUDE.md")) {
		l.Memory = append([]string{p}, l.Memory...)
	}
	if p := filepath.Join(c, "settings.json"); exists(p) {
		l.Settings = p
	}
	if p := filepath.Join(root, ".mcp.json"); exists(p) {
		l.MCP = p
	}
	return l
}
func detectCopilot(root string) copilotLayout {
	g := filepath.Join(root, ".github")
	l := copilotLayout{AgentsMD: walkNamed(root, "AGENTS.md", map[string]bool{".github": true}), Instructions: globFiles(filepath.Join(g, "instructions"), ".instructions.md"), Agents: globFiles(filepath.Join(g, "agents"), ".md"), Skills: append(skillsIn(filepath.Join(g, "skills")), skillsIn(filepath.Join(root, ".agents", "skills"))...), Prompts: globFiles(filepath.Join(g, "prompts"), ".prompt.md"), Hooks: globFiles(filepath.Join(g, "hooks"), ".json")}
	for _, v := range []struct {
		p *string
		s string
	}{{&l.RepoInstructions, filepath.Join(g, "copilot-instructions.md")}, {&l.Settings, filepath.Join(g, "copilot", "settings.json")}, {&l.MCP, filepath.Join(g, "mcp.json")}} {
		if exists(v.s) {
			*v.p = v.s
		}
	}
	return l
}
func (l claudeLayout) present() bool {
	return len(l.Memory)+len(l.Rules)+len(l.Agents)+len(l.Skills)+len(l.Commands) > 0 || l.Settings != "" || l.MCP != ""
}
func (l copilotLayout) present() bool {
	return len(l.AgentsMD)+len(l.Instructions)+len(l.Agents)+len(l.Skills)+len(l.Prompts)+len(l.Hooks) > 0 || l.RepoInstructions != "" || l.Settings != "" || l.MCP != ""
}
func exists(p string) bool { _, e := os.Stat(p); return e == nil }

func frontmatter(text string) (map[string]any, string) {
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return map[string]any{}, text
	}
	rest := text[4:]
	i := strings.Index(rest, "\n---")
	if i < 0 {
		return map[string]any{}, text
	}
	raw := rest[:i]
	body := strings.TrimLeft(rest[i+4:], "\r\n")
	var m map[string]any
	if yaml.Unmarshal([]byte(raw), &m) != nil || m == nil {
		return map[string]any{}, text
	}
	return m, body
}
func render(meta map[string]any, body string) string {
	if len(meta) == 0 {
		return body
	}
	b, _ := yaml.Marshal(meta)
	body = strings.TrimLeft(body, "\n")
	if body == "" {
		return "---\n" + string(b) + "---\n"
	}
	return "---\n" + string(b) + "---\n\n" + body
}
func firstParagraph(s string) string {
	heading := ""
	for _, line := range strings.Split(s, "\n") {
		x := strings.TrimSpace(line)
		if x == "" || strings.HasPrefix(x, "<!--") {
			continue
		}
		if strings.HasPrefix(x, "#") {
			if heading == "" {
				heading = strings.TrimSpace(strings.TrimLeft(x, "#"))
			}
			continue
		}
		if len(x) > 1024 {
			x = x[:1024]
		}
		return x
	}
	return heading
}
func marker(body, source, command string) string {
	return strings.TrimSpace(body) + "\n\n<!-- generated by agentconv from " + source + ". Edit that file and re-run `agentconv " + command + "`; do not edit this copy. -->\n"
}
func slug(s string) string {
	r := regexp.MustCompile(`[^a-z0-9]+`)
	x := strings.Trim(r.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if x == "" {
		return "unnamed"
	}
	return x
}
func stringList(v any) []string {
	switch x := v.(type) {
	case string:
		var out []string
		for _, s := range strings.Split(x, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		var out []string
		for _, s := range x {
			out = append(out, stringList(s)...)
		}
		return out
	default:
		if v == nil {
			return nil
		}
		return []string{fmt.Sprint(v)}
	}
}
func dedupe(x []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range x {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

var c2pTools = map[string]string{"Bash": "execute", "Read": "read", "NotebookRead": "read", "Edit": "edit", "MultiEdit": "edit", "Write": "edit", "NotebookEdit": "edit", "Grep": "search", "Glob": "search", "Task": "agent", "Agent": "agent", "WebFetch": "web", "WebSearch": "web", "TodoWrite": "todo", "AskUserQuestion": "ask_user"}
var p2cTools = map[string][]string{"execute": {"Bash"}, "shell": {"Bash"}, "bash": {"Bash"}, "powershell": {"Bash"}, "read": {"Read"}, "view": {"Read"}, "edit": {"Edit", "Write"}, "write": {"Write"}, "create": {"Write"}, "search": {"Grep", "Glob"}, "grep": {"Grep"}, "glob": {"Glob"}, "agent": {"Task"}, "web": {"WebFetch", "WebSearch"}, "todo": {"TodoWrite"}}

func claudeTools(v any) ([]string, []string) {
	var out, unknown []string
	for _, n := range stringList(v) {
		base := strings.Split(n, "(")[0]
		if x, ok := c2pTools[base]; ok {
			out = append(out, x)
		} else if strings.HasPrefix(base, "mcp__") || base == "*" {
			out = append(out, base)
		} else {
			out = append(out, n)
			unknown = append(unknown, n)
		}
	}
	return dedupe(out), unknown
}
func copilotTools(v any) ([]string, []string) {
	names := stringList(v)
	for _, n := range names {
		if n == "*" {
			return nil, nil
		}
	}
	var out, unknown []string
	for _, n := range names {
		if x, ok := p2cTools[strings.ToLower(n)]; ok {
			out = append(out, x...)
		} else if strings.HasPrefix(n, "mcp__") {
			out = append(out, n)
		} else {
			out = append(out, n)
			unknown = append(unknown, n)
		}
	}
	return dedupe(out), unknown
}

func copyTree(p *plan, src, dst, root string) {
	_ = filepath.WalkDir(src, func(path string, d fs.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(path)
		info, _ := d.Info()
		p.add(fileOp{Path: filepath.ToSlash(filepath.Join(dst, rel(src, path))), Content: b, Source: rel(root, path), Executable: info.Mode()&0111 != 0})
		return nil
	})
}
func c2p(root string, l claudeLayout) *plan {
	p := newPlan("to-copilot")
	for _, src := range l.Memory {
		b, _ := os.ReadFile(src)
		dir := filepath.Dir(src)
		if filepath.Base(dir) == ".claude" && filepath.Dir(dir) == root {
			dir = root
		}
		p.add(fileOp{Path: rel(root, filepath.Join(dir, "AGENTS.md")), Content: b, Source: rel(root, src)})
	}
	for _, src := range l.Rules {
		m, b := readFM(src)
		name := strings.TrimSuffix(rel(filepath.Join(root, ".claude", "rules"), src), ".md")
		out := map[string]any{"applyTo": "**"}
		if x := stringList(m["paths"]); len(x) > 0 {
			out["applyTo"] = strings.Join(x, ", ")
		}
		if x, ok := m["description"]; ok {
			out["description"] = x
		}
		p.add(fileOp{Path: ".github/instructions/" + slug(name) + ".instructions.md", Content: []byte(render(out, marker(b, rel(root, src), "to-copilot"))), Source: rel(root, src)})
	}
	for _, src := range l.Agents {
		m, b := readFM(src)
		id := strings.TrimSuffix(filepath.Base(src), ".md")
		out := map[string]any{"name": valueOr(m["name"], id), "description": valueOr(m["description"], valueOr(firstParagraph(b), id))}
		if x, ok := m["tools"]; ok {
			tools, u := claudeTools(x)
			out["tools"] = tools
			if len(u) > 0 {
				p.warn(fmt.Sprintf("%s: tools %v have no Copilot name; passed through verbatim", rel(root, src), u))
			}
		}
		if x, ok := m["model"].(string); ok && strings.Contains(x, "-") && !strings.Contains(x, "[") {
			out["model"] = x
		}
		if x, ok := m["effort"]; ok {
			out["reasoningEffort"] = x
		}
		p.add(fileOp{Path: ".github/agents/" + id + ".agent.md", Content: []byte(render(out, marker(b, rel(root, src), "to-copilot"))), Source: rel(root, src)})
	}
	for _, s := range l.Skills {
		copyTree(p, filepath.Dir(s), ".github/skills/"+filepath.Base(filepath.Dir(s)), root)
	}
	commands(p, l, root, ".github/skills", "to-copilot")
	copyClaudeJSON(p, l, root)
	return p
}
func codex(root string, l claudeLayout) *plan {
	p := newPlan("to-codex")
	for _, src := range l.Memory {
		b, _ := os.ReadFile(src)
		dir := filepath.Dir(src)
		if filepath.Base(dir) == ".claude" && filepath.Dir(dir) == root {
			dir = root
		}
		p.add(fileOp{Path: rel(root, filepath.Join(dir, "AGENTS.md")), Content: b, Source: rel(root, src)})
	}
	for _, s := range l.Skills {
		copyTree(p, filepath.Dir(s), ".agents/skills/"+filepath.Base(filepath.Dir(s)), root)
	}
	commands(p, l, root, ".agents/skills", "to-codex")
	if len(l.Rules) > 0 {
		p.warn(".claude/rules/: Codex has no path-scoped repository instruction format; skipped")
	}
	if len(l.Agents) > 0 {
		p.warn(".claude/agents/: Codex has no project subagent-file format; skipped")
	}
	if l.Settings != "" {
		p.warn(".claude/settings.json: Codex settings and hooks are user/environment configuration; skipped")
	}
	if l.MCP != "" {
		p.warn(".mcp.json: Codex MCP configuration is not a portable project file; skipped")
	}
	return p
}

// syncClaude keeps Claude as the source of truth.  Unlike a symlink or a
// "read this other file" stub, every generated AGENTS.md contains the actual
// instructions a target agent is expected to load.
func syncClaude(root string, l claudeLayout, target string) *plan {
	var full *plan
	switch target {
	case "codex":
		full = codex(root, l)
	case "copilot":
		full = c2p(root, l)
	default:
		return newPlan("sync-claude-to-" + target)
	}

	p := newPlan("sync-claude-to-" + target)
	for _, op := range full.Ops {
		// Copilot natively understands the remaining Claude layout, so its
		// compatibility layer is AGENTS.md plus path-scoped instructions.
		if target == "copilot" && !strings.HasSuffix(op.Path, "AGENTS.md") &&
			!strings.HasPrefix(op.Path, ".github/instructions/") {
			continue
		}
		if strings.HasSuffix(op.Path, "AGENTS.md") {
			contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(op.Source)))
			if err == nil {
				op.Content = []byte(marker(string(contents), op.Source,
					"sync --from claude --to "+target))
			}
		}
		op.Direction = p.Direction
		p.add(op)
	}
	p.Warnings = append(p.Warnings, full.Warnings...)
	p.Notes = append(p.Notes, full.Notes...)
	return p
}
func commands(p *plan, l claudeLayout, root, dst, command string) {
	base := filepath.Join(root, ".claude", "commands")
	for _, src := range l.Commands {
		m, b := readFM(src)
		name := slug(strings.TrimSuffix(rel(base, src), ".md"))
		out := map[string]any{"name": name, "description": valueOr(m["description"], valueOr(firstParagraph(b), "/"+name+" command"))}
		if command == "to-copilot" {
			for _, k := range []string{"argument-hint", "allowed-tools", "user-invocable", "disable-model-invocation"} {
				if x, ok := m[k]; ok {
					out[k] = x
				}
			}
		}
		p.add(fileOp{Path: dst + "/" + name + "/SKILL.md", Content: []byte(render(out, marker(b, rel(root, src), command))), Source: rel(root, src)})
	}
}
func copyClaudeJSON(p *plan, l claudeLayout, root string) {
	if l.MCP != "" {
		b, _ := os.ReadFile(l.MCP)
		var x map[string]any
		if json.Unmarshal(b, &x) == nil {
			out, _ := json.MarshalIndent(x, "", "  ")
			p.add(fileOp{Path: ".github/mcp.json", Content: append(out, '\n'), Source: rel(root, l.MCP)})
		}
	}
	if l.Settings != "" {
		var x map[string]any
		b, _ := os.ReadFile(l.Settings)
		if json.Unmarshal(b, &x) == nil {
			out := map[string]any{}
			for _, k := range []string{"companyAnnouncements", "disableAllHooks", "enabledPlugins", "extraKnownMarketplaces", "includeCoAuthoredBy", "model"} {
				if v, ok := x[k]; ok {
					out[k] = v
				}
			}
			if len(out) > 0 {
				z, _ := json.MarshalIndent(out, "", "  ")
				p.add(fileOp{Path: ".github/copilot/settings.json", Content: append(z, '\n'), Source: rel(root, l.Settings)})
			}
		}
	}
}
func p2c(root string, l copilotLayout) *plan {
	p := newPlan("to-claude")
	var sources []string
	for _, s := range l.AgentsMD {
		if filepath.Dir(s) == root {
			sources = append(sources, s)
		} else {
			memory(p, root, []string{s}, filepath.Join(filepath.Dir(s), "CLAUDE.md"))
		}
	}
	if l.RepoInstructions != "" {
		sources = append(sources, l.RepoInstructions)
	}
	if len(sources) > 0 {
		memory(p, root, sources, filepath.Join(root, "CLAUDE.md"))
	}
	for _, src := range l.Instructions {
		m, b := readFM(src)
		name := strings.TrimSuffix(rel(filepath.Join(root, ".github", "instructions"), src), ".instructions.md")
		out := map[string]any{}
		if v := stringList(m["applyTo"]); len(v) > 0 && !(len(v) == 1 && v[0] == "**") {
			out["paths"] = v
		}
		p.add(fileOp{Path: ".claude/rules/" + name + ".md", Content: []byte(render(out, marker(b, rel(root, src), "to-claude"))), Source: rel(root, src)})
	}
	for _, src := range l.Agents {
		m, b := readFM(src)
		name := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(src), ".agent.md"), ".md")
		out := map[string]any{"name": valueOr(m["name"], name), "description": valueOr(m["description"], valueOr(firstParagraph(b), name))}
		if x, ok := m["tools"]; ok {
			if tools, _ := copilotTools(x); tools != nil {
				out["tools"] = strings.Join(tools, ", ")
			}
		}
		if x, ok := m["model"]; ok {
			out["model"] = x
		}
		if x, ok := m["reasoningEffort"]; ok {
			out["effort"] = x
		}
		p.add(fileOp{Path: ".claude/agents/" + name + ".md", Content: []byte(render(out, marker(b, rel(root, src), "to-claude"))), Source: rel(root, src)})
	}
	for _, s := range l.Skills {
		copyTree(p, filepath.Dir(s), ".claude/skills/"+filepath.Base(filepath.Dir(s)), root)
	}
	for _, src := range l.Prompts {
		m, b := readFM(src)
		name := strings.TrimSuffix(rel(filepath.Join(root, ".github", "prompts"), src), ".prompt.md")
		out := map[string]any{}
		if x, ok := m["description"]; ok {
			out["description"] = x
		}
		if x, ok := m["tools"]; ok {
			if tools, _ := copilotTools(x); tools != nil {
				out["allowed-tools"] = strings.Join(tools, ", ")
			}
		}
		if x, ok := m["model"]; ok {
			out["model"] = x
		}
		p.add(fileOp{Path: ".claude/commands/" + name + ".md", Content: []byte(render(out, marker(b, rel(root, src), "to-claude"))), Source: rel(root, src)})
	}
	if l.MCP != "" {
		b, _ := os.ReadFile(l.MCP)
		var x map[string]any
		if json.Unmarshal(b, &x) == nil {
			if servers, ok := x["mcpServers"]; ok {
				x = map[string]any{"mcpServers": servers}
				z, _ := json.MarshalIndent(x, "", "  ")
				p.add(fileOp{Path: ".mcp.json", Content: append(z, '\n'), Source: rel(root, l.MCP)})
			}
		}
	}
	return p
}
func memory(p *plan, root string, sources []string, target string) {
	var parts []string
	for _, s := range sources {
		b, _ := os.ReadFile(s)
		parts = append(parts, strings.TrimSpace(string(b)))
	}
	p.add(fileOp{Path: rel(root, target), Content: []byte(strings.Join(parts, "\n\n") + "\n"), Source: strings.Join(relAll(root, sources), ", ")})
}
func relAll(root string, x []string) []string {
	for i := range x {
		x[i] = rel(root, x[i])
	}
	return x
}
func readFM(path string) (map[string]any, string) {
	b, _ := os.ReadFile(path)
	return frontmatter(string(b))
}
func valueOr(v any, fallback any) any {
	if v == nil || fmt.Sprint(v) == "" {
		return fallback
	}
	return v
}

func explicitConvert(root, from, to string) *plan {
	if from == "" || to == "" {
		die("convert requires both --from and --to")
	}
	switch {
	case from == "claude" && to == "copilot":
		l := detectClaude(root)
		if !l.present() {
			die("no Claude Code configuration found")
		}
		return c2p(root, l)
	case from == "claude" && to == "codex":
		l := detectClaude(root)
		if !l.present() {
			die("no Claude Code configuration found")
		}
		return codex(root, l)
	case from == "copilot" && to == "claude":
		l := detectCopilot(root)
		if !l.present() {
			die("no Copilot configuration found")
		}
		return p2c(root, l)
	default:
		die("unsupported conversion: " + from + " -> " + to)
	}
	return nil
}

func explicitSync(root, from, to string) *plan {
	if to == "" {
		die("sync requires --to")
	}
	if from == "" {
		c, k := detectClaude(root), detectCopilot(root)
		if c.present() == k.present() {
			die("could not infer one source convention; pass --from claude or --from copilot")
		}
		if c.present() {
			from = "claude"
		} else {
			from = "copilot"
		}
	}
	if from == "claude" && (to == "copilot" || to == "codex") {
		l := detectClaude(root)
		if !l.present() {
			die("no Claude Code configuration found")
		}
		return syncClaude(root, l, to)
	}
	// The reverse direction still uses the full translator until Codex exposes
	// an equivalent project-level source format for Copilot instructions.
	if from == "copilot" && to == "claude" {
		l := detectCopilot(root)
		if !l.present() {
			die("no Copilot configuration found")
		}
		return p2c(root, l)
	}
	die("unsupported sync: " + from + " -> " + to)
	return nil
}

func updateAsset() (string, error) {
	var osName string
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
		osName = runtime.GOOS
	default:
		return "", fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
	arch := runtime.GOARCH
	if arch == "x86_64" {
		arch = "amd64"
	}
	if arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}
	if osName == "windows" && arch != "amd64" {
		return "", fmt.Errorf("Windows arm64 releases are not available")
	}
	asset := fmt.Sprintf("agentconv-%s-%s", osName, arch)
	if osName == "windows" {
		asset += ".exe"
	}
	return asset, nil
}

func updateURL() (string, error) {
	asset, err := updateAsset()
	if err != nil {
		return "", err
	}
	return "https://github.com/" + releaseRepository + "/releases/latest/download/" + asset, nil
}

func confirmUpdate(in io.Reader, out io.Writer, executable, url string) bool {
	fmt.Fprintln(out, "This downloads the latest public agentconv release and replaces the current executable.")
	fmt.Fprintln(out, "Current executable:", executable)
	fmt.Fprintln(out, "Download:", url)
	fmt.Fprint(out, "Proceed? [y/N] ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && len(line) == 0 {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func update(dry bool) error {
	url, err := updateURL()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find current executable: %w", err)
	}
	if dry {
		fmt.Println("Would download", url)
		fmt.Println("Would replace", executable)
		return nil
	}

	response, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download latest release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download latest release: %s", response.Status)
	}
	temporary, err := os.CreateTemp(filepath.Dir(executable), ".agentconv-update-*")
	if err != nil {
		return fmt.Errorf("create update file: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if _, err = io.Copy(temporary, io.LimitReader(response.Body, 100<<20)); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write update: %w", err)
	}
	if err = temporary.Chmod(0755); err == nil {
		err = temporary.Close()
	}
	if err != nil {
		return fmt.Errorf("finalize update: %w", err)
	}

	if runtime.GOOS == "windows" {
		command := fmt.Sprintf(`ping 127.0.0.1 -n 2 >nul & move /Y "%s" "%s" >nul`, temporaryName, executable)
		if err := exec.Command("cmd.exe", "/C", command).Start(); err != nil {
			return fmt.Errorf("schedule Windows update: %w", err)
		}
		fmt.Println("Update downloaded. agentconv will be replaced after this command exits.")
		return nil
	}
	if err := os.Rename(temporaryName, executable); err != nil {
		return fmt.Errorf("replace executable: %w", err)
	}
	fmt.Printf("Updated agentconv to the latest release. Run `agentconv --version` to verify.\n")
	return nil
}

func usage() {
	fmt.Fprintf(os.Stderr, "agentconv %s\n\nUsage:\n  agentconv convert --from <claude|copilot> --to <codex|copilot|claude> [PATH]\n  agentconv sync [--from <claude|copilot>] --to <codex|copilot|claude> [PATH]\n  agentconv update [--dry-run] [--force]\n\nCompatibility commands: detect, to-copilot, to-codex, to-claude, universal, clean\n", version)
}
func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	cmd := os.Args[1]
	if cmd == "--version" {
		fmt.Println(version)
		return
	}
	if cmd == "--help" || cmd == "-h" {
		usage()
		return
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	dry := fs.Bool("dry-run", false, "show changes without writing")
	fs.BoolVar(dry, "n", false, "show changes without writing")
	force := fs.Bool("force", false, "overwrite protected generated files")
	fs.BoolVar(force, "f", false, "overwrite protected generated files")
	quiet := fs.Bool("quiet", false, "only warnings")
	fs.BoolVar(quiet, "q", false, "only warnings")
	from := fs.String("from", "", "source convention (claude or copilot)")
	to := fs.String("to", "", "target convention (claude, copilot, or codex)")
	// argparse accepted options on either side of the optional project path.
	// Normalize that shape before handing options to Go's flag package, which
	// otherwise stops parsing at the first positional argument.
	var options []string
	project := ""
	args := os.Args[2:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--from" || arg == "--to" {
			if i+1 == len(args) {
				die(arg + " requires a value")
			}
			options = append(options, arg, args[i+1])
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			options = append(options, arg)
		} else if project == "" {
			project = arg
		} else {
			fs.Usage()
			os.Exit(2)
		}
	}
	_ = fs.Parse(options)
	root := "."
	if project != "" {
		root = project
	}
	var e error
	root, e = filepath.Abs(root)
	if e != nil || !exists(root) {
		fmt.Fprintln(os.Stderr, "not a directory:", root)
		os.Exit(2)
	}
	if cmd == "detect" {
		c, k := detectClaude(root), detectCopilot(root)
		fmt.Printf("project: %s\nclaude:  %s\ncopilot: %s\n", root, yesno(c.present()), yesno(k.present()))
		return
	}
	if cmd == "clean" {
		if !*dry && !*force && !confirmClean(os.Stdin, os.Stdout, root) {
			fmt.Fprintln(os.Stderr, "cancelled")
			return
		}
		clean(root, *dry)
		return
	}
	if cmd == "update" {
		url, updateErr := updateURL()
		if updateErr != nil {
			die(updateErr.Error())
		}
		executable, updateErr := os.Executable()
		if updateErr != nil {
			die(updateErr.Error())
		}
		if !*dry && !*force && !confirmUpdate(os.Stdin, os.Stdout, executable, url) {
			fmt.Fprintln(os.Stderr, "cancelled")
			return
		}
		if updateErr := update(*dry); updateErr != nil {
			die(updateErr.Error())
		}
		return
	}
	var p *plan
	switch cmd {
	case "to-copilot":
		l := detectClaude(root)
		if !l.present() {
			die("no Claude Code configuration found")
		}
		p = c2p(root, l)
	case "to-codex":
		l := detectClaude(root)
		if !l.present() {
			die("no Claude Code configuration found")
		}
		p = codex(root, l)
	case "to-claude":
		l := detectCopilot(root)
		if !l.present() {
			die("no Copilot configuration found")
		}
		p = p2c(root, l)
	case "convert":
		p = explicitConvert(root, *from, *to)
	case "sync":
		p = explicitSync(root, *from, *to)
	case "universal":
		c, k := detectClaude(root), detectCopilot(root)
		if c.present() && !k.present() {
			p = syncClaude(root, c, "copilot")
		} else if k.present() && !c.present() {
			p = p2c(root, k)
		} else if c.present() && k.present() {
			die("both Claude and Copilot layouts are present; use an explicit conversion command")
		} else {
			die("no Claude Code or Copilot configuration found")
		}
	default:
		usage()
		os.Exit(2)
	}
	if !*dry && !*force && !confirm(os.Stdin, os.Stdout, p) {
		fmt.Fprintln(os.Stderr, "cancelled")
		return
	}
	r, e := apply(p, root, *force, *dry)
	if e != nil {
		die(e.Error())
	}
	if !*quiet {
		printResult(r, *dry)
	}
	for _, x := range p.Notes {
		fmt.Fprintln(os.Stderr, "note:", x)
	}
	for _, x := range p.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", x)
	}
}

func confirm(in io.Reader, out io.Writer, p *plan) bool {
	fmt.Fprintf(out, "This will write or refresh up to %d generated project file(s).\n", len(p.Ops))
	fmt.Fprintln(out, "Existing files not generated by agentconv are protected unless --force is used.")
	fmt.Fprintln(out, "Previously generated files whose source disappeared may be removed if they are unchanged.")
	if len(p.Warnings) > 0 {
		fmt.Fprintln(out, "Important conversion implications:")
		for _, warning := range p.Warnings {
			fmt.Fprintf(out, "  - %s\n", warning)
		}
	}
	fmt.Fprint(out, "Proceed? [y/N] ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && len(line) == 0 {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func confirmClean(in io.Reader, out io.Writer, root string) bool {
	m := loadManifest(root)
	fmt.Fprintf(out, "This will remove up to %d files previously generated by agentconv.\n", len(m.Generated))
	fmt.Fprintln(out, "Files changed since generation are retained. Use --force to skip this confirmation.")
	fmt.Fprint(out, "Proceed? [y/N] ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && len(line) == 0 {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func clean(root string, dry bool) {
	m := loadManifest(root)
	for path, rec := range m.Generated {
		b, e := os.ReadFile(filepath.Join(root, path))
		if e == nil && digest(b) == rec.SHA {
			if dry {
				fmt.Println("  would remove ", path)
			} else {
				_ = os.Remove(filepath.Join(root, path))
				fmt.Println("  removed ", path)
			}
		}
	}
}
func yesno(x bool) string {
	if x {
		return "yes"
	}
	return "no"
}
func die(s string) { fmt.Fprintln(os.Stderr, s); os.Exit(1) }
func printResult(r result, dry bool) {
	verb := "wrote"
	if dry {
		verb = "would write"
	}
	for _, s := range r.Written {
		fmt.Printf("  %-11s %s\n", verb, s)
	}
	for _, s := range r.Unchanged {
		fmt.Printf("  unchanged   %s\n", s)
	}
	for _, s := range r.Skipped {
		fmt.Printf("  skipped     %s (exists and was not generated by agentconv)\n", s)
	}
	for _, s := range r.Modified {
		fmt.Printf("  skipped     %s (edited since agentconv generated it)\n", s)
	}
	for _, s := range r.Removed {
		fmt.Printf("  removed     %s\n", s)
	}
	for _, s := range r.Stale {
		fmt.Printf("  kept        %s (edited since agentconv generated it)\n", s)
	}
}
