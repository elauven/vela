package gen

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ConfigFile is the configuration of the app layer, next to
// vela.config.json.
const ConfigFile = "vela.app.json"

// Config locates the inputs and outputs of the generator. Paths are
// slash-separated and relative to Root.
type Config struct {
	// Root is the project directory: the one with vela.config.json and
	// go.mod.
	Root string `json:"-"`

	Routes       string `json:"routes"`       // routes tree, "<project>/routes"
	Server       string `json:"server"`       // mirrored Go tree, "server/routes"
	Params       string `json:"params"`       // JavaScript matchers, "<project>/params"
	ServerParams string `json:"serverParams"` // Go matchers, "server/params"
	Out          string `json:"out"`          // generated client sources, "<project>/_app"
	GoPackage    string `json:"goPackage"`    // generated Go route table, "internal/appgen"
	GoModule     string `json:"goModule"`     // module of go.mod
	// Project is the source root of vela.config.json ("src"); component
	// and segment ids are relative to it.
	Project string `json:"project"`
	// SSRModule is the import root of the generated renderers
	// (vela.config.json "ssrModule").
	SSRModule string `json:"ssrModule"`
}

// LoadConfig reads vela.app.json, vela.config.json and go.mod in root and
// fills the defaults (docs/APP.md P-1, P-14, P-16).
func LoadConfig(root string) (Config, []Diagnostic) {
	cfg := Config{Root: root}
	var diags []Diagnostic
	fail := func(file, format string, args ...any) {
		diags = append(diags, Diagnostic{File: file, Code: CodeConfig, Message: fmt.Sprintf(format, args...)})
	}
	var vela struct {
		Project   string `json:"project"`
		SSRModule string `json:"ssrModule"`
	}
	if data, err := os.ReadFile(filepath.Join(root, "vela.config.json")); err == nil {
		if err := json.Unmarshal(data, &vela); err != nil {
			fail("vela.config.json", "%v", err)
		}
	} else if !os.IsNotExist(err) {
		fail("vela.config.json", "%v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, ConfigFile)); err == nil {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&cfg); err != nil {
			fail(ConfigFile, "%v", err)
		}
	} else if !os.IsNotExist(err) {
		fail(ConfigFile, "%v", err)
	}
	cfg.Root = root
	if cfg.Project == "" {
		cfg.Project = vela.Project
	}
	if cfg.Project == "" {
		cfg.Project = "src"
	}
	if cfg.SSRModule == "" {
		cfg.SSRModule = vela.SSRModule
	}
	if cfg.GoModule == "" {
		module, err := goModulePath(filepath.Join(root, "go.mod"))
		if err != nil {
			fail("go.mod", "%v; set \"goModule\" in %s", err, ConfigFile)
		}
		cfg.GoModule = module
	}
	defaults := []struct {
		field *string
		value string
	}{
		{&cfg.Routes, path.Join(cfg.Project, "routes")},
		{&cfg.Server, "server/routes"},
		{&cfg.Params, path.Join(cfg.Project, "params")},
		{&cfg.ServerParams, "server/params"},
		{&cfg.Out, path.Join(cfg.Project, "_app")},
		{&cfg.GoPackage, "internal/appgen"},
	}
	for _, d := range defaults {
		if *d.field == "" {
			*d.field = d.value
		}
		*d.field = path.Clean(filepath.ToSlash(*d.field))
	}
	cfg.Project = path.Clean(filepath.ToSlash(cfg.Project))
	if cfg.SSRModule == "" {
		fail("vela.config.json", "\"ssrModule\" is not set; the Go route table imports the generated VelaApp renderer from it (for example %q)", cfg.GoModule+"/out/server")
	}
	for _, d := range []struct{ name, value string }{{"routes", cfg.Routes}, {"params", cfg.Params}, {"out", cfg.Out}} {
		if !within(cfg.Project, d.value) {
			fail(ConfigFile, "%q (%s) must be inside the project directory %q, which vela compiles", d.name, d.value, cfg.Project)
		}
	}
	for _, d := range []struct{ name, value string }{{"server", cfg.Server}, {"serverParams", cfg.ServerParams}, {"goPackage", cfg.GoPackage}} {
		if path.IsAbs(d.value) || d.value == ".." || strings.HasPrefix(d.value, "../") {
			fail(ConfigFile, "%q (%s) must be inside the Go module", d.name, d.value)
		}
	}
	return cfg, diags
}

// within reports whether rel is dir or below it.
func within(dir, rel string) bool {
	return rel == dir || strings.HasPrefix(rel, dir+"/") || dir == "."
}

func goModulePath(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if rest, ok := strings.CutPrefix(line, "module"); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	return "", fmt.Errorf("no module line")
}

// abs is a config path on disk.
func (c Config) abs(rel string) string {
	return filepath.Join(c.Root, filepath.FromSlash(rel))
}

// importPath is the Go import path of a module-relative directory.
func (c Config) importPath(rel string) string {
	if rel == "." {
		return c.GoModule
	}
	return c.GoModule + "/" + rel
}

// componentID is the $$id of a component (P-11): its path relative to the
// project directory, without the extension.
func (c Config) componentID(rel string) string {
	id := strings.TrimPrefix(rel, c.Project+"/")
	if c.Project == "." {
		id = rel
	}
	return strings.TrimSuffix(id, path.Ext(id))
}
