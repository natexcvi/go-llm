package capabilities

import (
	"context"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	h "github.com/natexcvi/go-llm"
	"github.com/natexcvi/go-llm/workspace"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
)

type skill struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	When        string `yaml:"when_to_use"`
	Disable     string `yaml:"disable-model-invocation"`
	dir         string
	body        string
}

func parseSkill(dir, markdown string) (skill, bool) {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	var s skill
	if !utf8.ValidString(markdown) || lines[0] != "---" {
		return s, false
	}
	end := slices.Index(lines[1:], "---")
	if end < 0 || yaml.Unmarshal([]byte(strings.Join(lines[1:1+end], "\n")), &s) != nil {
		return s, false
	}
	s.Description = strings.TrimSpace(s.Description)
	if s.Description == "" || s.Disable != "" && !strings.EqualFold(s.Disable, "false") {
		return s, false
	}
	name := norm.NFKC.String(path.Base(dir))
	if s.Name != "" && norm.NFKC.String(s.Name) != name {
		return s, false
	}
	if utf8.RuneCountInString(name) > 64 || name != strings.ToLower(name) || strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") || strings.Contains(name, "--") {
		return s, false
	}
	if strings.IndexFunc(name, func(r rune) bool { return r != '-' && !unicode.IsLetter(r) && !unicode.IsNumber(r) }) >= 0 {
		return s, false
	}
	s.dir = dir
	s.Name = name
	s.body = fmt.Sprintf("# Skill: %s\n\nSkill directory: `%s`. Relative paths in this skill resolve against it.", name, dir)
	body := strings.TrimSpace(strings.Join(lines[end+2:], "\n"))
	if body != "" {
		s.body += "\n\n" + strings.NewReplacer("${SKILL_DIR}", dir, "${CLAUDE_SKILL_DIR}", dir).Replace(body)
	}
	return s, true
}

// Skills discovers Agent Skills in immediate subdirectories. It advertises their
// metadata at run start and serves the scanned Markdown body on demand.
// Invalid, flagged and model-disabled packages are excluded from the catalog.
func Skills(files fs.FS, dir string, checks ...ArtifactCheck) h.Capability {
	return SkillCatalog{Directories: []string{dir}}.Capability(files, checks...)
}

// SkillCatalog discovers libraries in precedence order. A nil Include selects
// every skill, while a non-nil empty Include selects none.
type SkillCatalog struct {
	Directories, Include, Exclude       []string
	SkipMissing, KeepFirst, SkipUnknown bool
	// ReadFiles enables paginated UTF-8 reads confined to loaded skill packages.
	ReadFiles bool
}

func (cfg SkillCatalog) Capability(files fs.FS, checks ...ArtifactCheck) h.Capability {
	cfg.Directories = slices.Clone(cfg.Directories)
	cfg.Include = slices.Clone(cfg.Include)
	cfg.Exclude = slices.Clone(cfg.Exclude)
	checks = slices.Clone(checks)
	return func(ctx context.Context, r *h.Run) error {
		if files == nil {
			return errors.New("skill filesystem required")
		}
		if cfg.Include != nil && cfg.Exclude != nil {
			return errors.New("choose include or exclude for skills")
		}
		skills := map[string]skill{}
		digests := map[string][32]byte{}
		for _, dir := range cfg.Directories {
			entries, err := fs.ReadDir(files, dir)
			if errors.Is(err, fs.ErrNotExist) && cfg.SkipMissing {
				continue
			}
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				if !entry.IsDir() {
					continue
				}
				p := path.Join(dir, entry.Name())
				b, err := workspace.ReadFile(files, path.Join(p, "SKILL.md"), 128<<10)
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				if err != nil {
					return err
				}
				s, ok := parseSkill(p, string(b))
				if !ok || checkArtifact(checks, s.Name, string(b)) != nil {
					continue
				}
				digest := sha256.Sum256(b)
				if prior, exists := digests[s.Name]; exists {
					if prior == digest || cfg.KeepFirst {
						continue
					}
					return fmt.Errorf("duplicate skill %q", s.Name)
				}
				digests[s.Name] = digest
				skills[s.Name] = s
			}
		}
		if !cfg.SkipUnknown {
			for _, names := range [][]string{cfg.Include, cfg.Exclude} {
				for _, name := range names {
					if _, ok := skills[name]; !ok {
						return fmt.Errorf("unknown skill %q", name)
					}
				}
			}
		}
		var catalog []string
		for name, s := range skills {
			if cfg.Include != nil && !slices.Contains(cfg.Include, name) || slices.Contains(cfg.Exclude, name) {
				delete(skills, name)
				continue
			}
			catalog = append(catalog, strings.TrimSpace(s.Name+": "+s.Description+" "+strings.TrimSpace(s.When)))
		}
		slices.Sort(catalog)
		if len(catalog) == 0 {
			return nil
		}
		r.Instructions = append(r.Instructions, "Available skills (use load_capability to read one):\n"+strings.Join(catalog, "\n"))
		var loaded map[string][32]byte
		if cfg.ReadFiles {
			loaded = map[string][32]byte{}
			if err := h.BindState(r, "skills.loaded", &loaded); err != nil {
				return err
			}
			if err := r.Register(skillReader(files, skills, digests, loaded, checks)); err != nil {
				return err
			}
		}
		return r.Register(h.NewTool("load_capability", "Load an advertised skill by name.", func(_ context.Context, in struct {
			Name string `json:"name"`
		}) (string, error) {
			s, ok := skills[in.Name]
			if !ok {
				return "", h.Retry(errors.New("unknown skill"))
			}
			if cfg.ReadFiles {
				loaded[in.Name] = digests[in.Name]
			}
			return s.body, nil
		}))
	}
}

func skillReader(files fs.FS, skills map[string]skill, digests, loaded map[string][32]byte, checks []ArtifactCheck) h.Tool {
	return h.NewTool("read_skill_file", "Read UTF-8 text from a loaded skill. Offset is in characters.", func(ctx context.Context, in struct {
		Name   string `json:"name"`
		Path   string `json:"path"`
		Offset int    `json:"offset,omitempty"`
	}) (string, error) {
		s, ok := skills[in.Name]
		if !ok || loaded[in.Name] != digests[in.Name] {
			return "", h.Retry(errors.New("load the skill first"))
		}
		if !fs.ValidPath(in.Path) || in.Path == "." || in.Offset < 0 {
			return "", h.Retry(fs.ErrInvalid)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		const limit = 10 << 20
		var data []byte
		var err error
		switch f := files.(type) {
		case interface {
			ReadWithin(string, string, int64) ([]byte, error)
		}:
			data, err = f.ReadWithin(s.dir, in.Path, limit)
		case embed.FS:
			data, err = workspace.ReadFile(f, path.Join(s.dir, in.Path), limit)
		default:
			return "", h.Retry(errors.New("skill filesystem does not support confined resource reads"))
		}
		if err != nil {
			return "", h.Retry(err)
		}
		if !utf8.Valid(data) {
			return "", h.Retry(errors.New("skill resource requires UTF-8 text"))
		}
		if err := checkArtifact(checks, in.Path, string(data)); err != nil {
			return "", h.Retry(err)
		}
		runes := []rune(string(data))
		if in.Offset > len(runes) {
			return "", h.Retry(errors.New("offset beyond end of resource"))
		}
		end := in.Offset + min(50000, len(runes)-in.Offset)
		page := string(runes[in.Offset:end])
		if end < len(runes) {
			page += fmt.Sprintf("\n[Continue at offset %d]", end)
		}
		return page, nil
	})
}
