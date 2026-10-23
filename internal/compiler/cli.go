package compiler

import (
	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/runtime"

	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ConfigFileName is the project configuration the CLI and the compiler
// service read from the working directory (task 060).
const ConfigFileName = "vela.config.json"

// fileFlags are the flags that shape the compilation of one component. The
// build command, `vela compile` and `vela serve` share them; a request of the
// service sets them through ServiceRequest.Options.
type fileFlags struct {
	minify                  *bool
	cssMode                 *string
	sourceMap               *bool
	target                  *string
	unavailableCapabilities *string
	strict                  *bool
	hydrationVerify         *string
	mode                    *string
	runtimeImport           *string
	checkDeps               *bool
	componentImports        *string
}

// fileFlagDefaults differ between the build command (CSS injected, runtime
// beside the output) and the compiler service, whose clients bundle.
type fileFlagDefaults struct {
	cssMode       string
	runtimeImport string
}

var BuildFlagDefaults = fileFlagDefaults{cssMode: "inject", runtimeImport: "relative"}

func AddFileFlags(fs *flag.FlagSet, defaults fileFlagDefaults) *fileFlags {
	return &fileFlags{
		minify:                  fs.Bool("minify", false, "minify output JS"),
		cssMode:                 fs.String("css", defaults.cssMode, "CSS delivery: 'inject' (CSS-in-JS) or 'external' (sibling .css files)"),
		sourceMap:               fs.Bool("sourcemap", false, "emit an external v3 source map alongside AST-generated JS"),
		target:                  fs.String("target", "permissive", "JavaScript host profile: permissive, browser, worker, node, or ssr"),
		unavailableCapabilities: fs.String("unavailable-capabilities", "", "comma-separated direct host capabilities to reject, e.g. fetch,indexedDB"),
		strict:                  fs.Bool("strict", false, "treat proven unresolved JavaScript reads as errors"),
		hydrationVerify:         fs.String("hydration-verify", "", "development hydration value verification: repair (default), fail, or off"),
		mode:                    fs.String("mode", "development", "build mode: 'development' (default) or 'production'"),
		runtimeImport:           fs.String("runtime-import", defaults.runtimeImport, "runtime imports of generated modules: 'relative' (the runtime is written beside the output) or 'bare' (import "+runtime.PackageName+", installed by the application, for bundlers)"),
		checkDeps:               fs.Bool("check-deps", false, "warn when a package import does not resolve in node_modules (the compiler never resolves or bundles packages)"),
		componentImports:        fs.String("component-imports", "js", "imports of .vela components in generated modules: 'js' (the compiled module) or 'vela' (the component, for bundler plugins that compile it)"),
	}
}

// options validates the flags and returns the compile options they select.
func (f *fileFlags) Options() (CompileOptions, error) {
	switch *f.runtimeImport {
	case "relative", "bare":
	default:
		return CompileOptions{}, fmt.Errorf("invalid --runtime-import value %q (want 'relative' or 'bare')", *f.runtimeImport)
	}
	targetProfile, err := analyzer.ParseTargetProfile(*f.target)
	if err != nil {
		return CompileOptions{}, err
	}
	switch *f.mode {
	case "development", "production":
	default:
		return CompileOptions{}, fmt.Errorf("invalid --mode value %q (want 'development' or 'production')", *f.mode)
	}
	switch *f.hydrationVerify {
	case "", "repair", "fail", "off":
	default:
		return CompileOptions{}, fmt.Errorf("invalid --hydration-verify value %q (want repair, fail, or off)", *f.hydrationVerify)
	}
	if *f.hydrationVerify != "" && *f.mode == "production" {
		// Production builds contain no verification code (V-11.3a, V-11.3c).
		return CompileOptions{}, errors.New("--hydration-verify cannot be combined with --mode=production")
	}
	switch *f.cssMode {
	case "inject", "external":
	default:
		return CompileOptions{}, fmt.Errorf("invalid --css value %q (want 'inject' or 'external')", *f.cssMode)
	}
	switch *f.componentImports {
	case "js", "vela":
	default:
		return CompileOptions{}, fmt.Errorf("invalid --component-imports value %q (want 'js' or 'vela')", *f.componentImports)
	}
	opts := CompileOptions{
		Minify:                  *f.minify,
		CSSExternal:             *f.cssMode == "external",
		SourceMap:               *f.sourceMap,
		TargetProfile:           targetProfile,
		UnavailableCapabilities: SplitCapabilityList(*f.unavailableCapabilities),
		Strict:                  *f.strict,
		HydrationVerify:         *f.hydrationVerify,
		Production:              *f.mode == "production",
		BareRuntimeImports:      *f.runtimeImport == "bare",
		CheckDeps:               *f.checkDeps,
		SourceComponentImports:  *f.componentImports == "vela",
	}
	if opts.BareRuntimeImports {
		opts.RuntimeImport = runtime.PackageName
	}
	return opts, nil
}

// FileOptions are the per-component settings of vela.config.json and of a
// service request; each field is the flag of the same name.
type FileOptions struct {
	Minify                  *bool    `json:"minify,omitempty"`
	CSS                     *string  `json:"css,omitempty"`
	SourceMap               *bool    `json:"sourcemap,omitempty"`
	Target                  *string  `json:"target,omitempty"`
	UnavailableCapabilities []string `json:"unavailableCapabilities,omitempty"`
	Strict                  *bool    `json:"strict,omitempty"`
	HydrationVerify         *string  `json:"hydrationVerify,omitempty"`
	Mode                    *string  `json:"mode,omitempty"`
	RuntimeImport           *string  `json:"runtimeImport,omitempty"`
	CheckDeps               *bool    `json:"checkDeps,omitempty"`
	ComponentImports        *string  `json:"componentImports,omitempty"`
}

// velaConfig is vela.config.json. Paths are relative to the file.
type velaConfig struct {
	Schema    string  `json:"$schema,omitempty"`
	Project   *string `json:"project,omitempty"`
	Out       *string `json:"out,omitempty"`
	SSR       *bool   `json:"ssr,omitempty"`
	SSRModule *string `json:"ssrModule,omitempty"`
	FileOptions
}

type FlagValue struct{ Name, Value string }

func (o FileOptions) flagValues() []FlagValue {
	var out []FlagValue
	addBool := func(name string, v *bool) {
		if v != nil {
			out = append(out, FlagValue{name, strconv.FormatBool(*v)})
		}
	}
	addString := func(name string, v *string) {
		if v != nil {
			out = append(out, FlagValue{name, *v})
		}
	}
	addBool("minify", o.Minify)
	addString("css", o.CSS)
	addBool("sourcemap", o.SourceMap)
	addString("target", o.Target)
	if o.UnavailableCapabilities != nil {
		out = append(out, FlagValue{"unavailable-capabilities", strings.Join(o.UnavailableCapabilities, ",")})
	}
	addBool("strict", o.Strict)
	addString("hydration-verify", o.HydrationVerify)
	addString("mode", o.Mode)
	addString("runtime-import", o.RuntimeImport)
	addBool("check-deps", o.CheckDeps)
	addString("component-imports", o.ComponentImports)
	return out
}

func (c *velaConfig) FlagValues() []FlagValue {
	var out []FlagValue
	if c.Project != nil {
		out = append(out, FlagValue{"project", *c.Project})
	}
	if c.Out != nil {
		out = append(out, FlagValue{"out", *c.Out})
	}
	if c.SSR != nil {
		out = append(out, FlagValue{"ssr", strconv.FormatBool(*c.SSR)})
	}
	if c.SSRModule != nil {
		out = append(out, FlagValue{"ssr-module", *c.SSRModule})
	}
	return append(out, c.FileOptions.flagValues()...)
}

// DecodeStrict decodes one JSON value and rejects unknown keys and
// trailing data.
func DecodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON value")
	}
	return nil
}

// LoadConfig reads the configuration at path, or vela.config.json in the
// working directory when path is empty; a missing default file is no
// configuration. Relative paths of the file become relative to the working
// directory.
func LoadConfig(path string) (*velaConfig, error) {
	explicit := path != ""
	if !explicit {
		path = ConfigFileName
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	config := &velaConfig{}
	if err := DecodeStrict(data, config); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	dir := filepath.Dir(path)
	for _, p := range []*string{config.Project, config.Out} {
		if p != nil && !filepath.IsAbs(*p) {
			*p = filepath.Join(dir, *p)
		}
	}
	return config, nil
}

// ApplyFlagValues sets the flags of fs that the command line left unset.
// Values for flags fs does not define are ignored, so one configuration
// serves every command.
// Errors name source, the file or request the values come from.
func ApplyFlagValues(fs *flag.FlagSet, source string, values []FlagValue) error {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	for _, v := range values {
		if set[v.Name] || fs.Lookup(v.Name) == nil {
			continue
		}
		if err := fs.Set(v.Name, v.Value); err != nil {
			return fmt.Errorf("%s: invalid value %q for %s: %v", source, v.Value, v.Name, err)
		}
	}
	return nil
}
