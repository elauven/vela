package ssr

import (
	"fmt"
	"go/build"
	"go/importer"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// goImporter is the importer of the standard library for type-checking
// generated Go and host packages.
//
// The packages that generated code and the runtime's type description use
// (io, context, fmt, encoding/json) are described here, so the type check
// does not depend on the Go installation: a release binary of vela is
// built by one Go version, and the export data of another version's
// standard library cannot be read by it (the compiled packages of a newer
// Go make an older go/importer fail). Other standard packages, which only
// host code imports, come from the installed Go; reading them never
// panics, and a failure is an import error.
func goImporter() types.Importer {
	return &stdImporter{packages: map[string]*types.Package{}}
}

// hostGoImporter is the importer of the standard library for host
// packages, the developer's Go code: the installed Go first, which has
// every name, and the built-in descriptions when it cannot be read.
func hostGoImporter() types.Importer {
	return &stdImporter{packages: map[string]*types.Package{}, installedFirst: true}
}

type stdImporter struct {
	packages       map[string]*types.Package
	real           types.Importer
	installedFirst bool
}

func (s *stdImporter) Import(importPath string) (*types.Package, error) {
	if pkg, ok := s.packages[importPath]; ok {
		return pkg, nil
	}
	if s.installedFirst {
		if pkg, err := s.installed(importPath); err == nil {
			s.packages[importPath] = pkg
			return pkg, nil
		}
	}
	var pkg *types.Package
	switch importPath {
	case "io":
		pkg = stdIOPackage()
	case "context":
		pkg = stdContextPackage()
	case "fmt":
		pkg = stdFmtPackage()
	case "encoding/json":
		pkg = stdJSONPackage()
	default:
		var err error
		if pkg, err = s.installed(importPath); err != nil {
			return nil, err
		}
	}
	s.packages[importPath] = pkg
	return pkg, nil
}

// installed imports a package from the export data of the installed Go.
func (s *stdImporter) installed(importPath string) (pkg *types.Package, err error) {
	if s.real == nil {
		findGOROOT.Do(discoverGOROOT)
		s.real = importer.Default()
	}
	defer func() {
		if r := recover(); r != nil {
			pkg, err = nil, fmt.Errorf("cannot read %q from the installed Go (%v); vela was built with another Go version", importPath, r)
		}
	}()
	return s.real.Import(importPath)
}

var findGOROOT sync.Once

// discoverGOROOT sets the GOROOT of build.Default, which a binary built
// with -trimpath (the release binaries) does not know unless GOROOT is set,
// from the go command on PATH.
func discoverGOROOT() {
	if build.Default.GOROOT != "" {
		if _, err := os.Stat(filepath.Join(build.Default.GOROOT, "src")); err == nil {
			return
		}
	}
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return
	}
	if goroot := strings.TrimSpace(string(out)); goroot != "" {
		build.Default.GOROOT = goroot
	}
}

// goToolchainHint explains an import failure of the standard library when
// no GOROOT was found.
func goToolchainHint() string {
	if build.Default.GOROOT != "" {
		return ""
	}
	return "; vela --ssr needs the Go toolchain: put go on PATH or set GOROOT"
}

func stdVar(pkg *types.Package, name string, t types.Type) *types.Var {
	return types.NewVar(token.NoPos, pkg, name, t)
}

func stdTuple(vars ...*types.Var) *types.Tuple { return types.NewTuple(vars...) }

func stdFunc(pkg *types.Package, name string, params, results *types.Tuple, variadic bool) {
	pkg.Scope().Insert(types.NewFunc(token.NoPos, pkg, name, types.NewSignatureType(nil, nil, nil, params, results, variadic)))
}

var (
	stdError = types.Universe.Lookup("error").Type()
	stdAny   = types.Universe.Lookup("any").Type()
	stdBytes = types.NewSlice(types.Typ[types.Byte])
)

// stdInterface declares the named interface name with the methods.
func stdInterface(pkg *types.Package, name string, methods func(pkg *types.Package) []*types.Func) {
	iface := types.NewInterfaceType(methods(pkg), nil)
	iface.Complete()
	pkg.Scope().Insert(types.NewTypeName(token.NoPos, pkg, name, nil))
	types.NewNamed(pkg.Scope().Lookup(name).(*types.TypeName), iface, nil)
}

func stdMethod(pkg *types.Package, name string, params, results *types.Tuple) *types.Func {
	return types.NewFunc(token.NoPos, pkg, name, types.NewSignatureType(nil, nil, nil, params, results, false))
}

func stdIOPackage() *types.Package {
	pkg := types.NewPackage("io", "io")
	stdInterface(pkg, "Writer", func(pkg *types.Package) []*types.Func {
		return []*types.Func{stdMethod(pkg, "Write",
			stdTuple(stdVar(pkg, "p", stdBytes)),
			stdTuple(stdVar(pkg, "n", types.Typ[types.Int]), stdVar(pkg, "err", stdError)))}
	})
	stdInterface(pkg, "Reader", func(pkg *types.Package) []*types.Func {
		return []*types.Func{stdMethod(pkg, "Read",
			stdTuple(stdVar(pkg, "p", stdBytes)),
			stdTuple(stdVar(pkg, "n", types.Typ[types.Int]), stdVar(pkg, "err", stdError)))}
	})
	pkg.Scope().Insert(types.NewVar(token.NoPos, pkg, "EOF", stdError))
	stdFunc(pkg, "WriteString", stdTuple(stdVar(pkg, "w", pkg.Scope().Lookup("Writer").Type()), stdVar(pkg, "s", types.Typ[types.String])),
		stdTuple(stdVar(pkg, "n", types.Typ[types.Int]), stdVar(pkg, "err", stdError)), false)
	pkg.MarkComplete()
	return pkg
}

func stdContextPackage() *types.Package {
	pkg := types.NewPackage("context", "context")
	done := types.NewChan(types.RecvOnly, types.NewStruct(nil, nil))
	stdInterface(pkg, "Context", func(pkg *types.Package) []*types.Func {
		return []*types.Func{
			stdMethod(pkg, "Done", nil, stdTuple(stdVar(pkg, "", done))),
			stdMethod(pkg, "Err", nil, stdTuple(stdVar(pkg, "", stdError))),
			stdMethod(pkg, "Value", stdTuple(stdVar(pkg, "key", stdAny)), stdTuple(stdVar(pkg, "", stdAny))),
		}
	})
	ctx := pkg.Scope().Lookup("Context").Type()
	stdFunc(pkg, "Background", nil, stdTuple(stdVar(pkg, "", ctx)), false)
	stdFunc(pkg, "TODO", nil, stdTuple(stdVar(pkg, "", ctx)), false)
	pkg.Scope().Insert(types.NewVar(token.NoPos, pkg, "Canceled", stdError))
	pkg.MarkComplete()
	return pkg
}

func stdFmtPackage() *types.Package {
	pkg := types.NewPackage("fmt", "fmt")
	format := stdVar(pkg, "format", types.Typ[types.String])
	args := stdVar(pkg, "a", types.NewSlice(stdAny))
	stdFunc(pkg, "Errorf", stdTuple(format, args), stdTuple(stdVar(pkg, "", stdError)), true)
	stdFunc(pkg, "Sprintf", stdTuple(format, args), stdTuple(stdVar(pkg, "", types.Typ[types.String])), true)
	stdFunc(pkg, "Sprint", stdTuple(args), stdTuple(stdVar(pkg, "", types.Typ[types.String])), true)
	pkg.MarkComplete()
	return pkg
}

func stdJSONPackage() *types.Package {
	pkg := types.NewPackage("encoding/json", "json")
	name := types.NewTypeName(token.NoPos, pkg, "RawMessage", nil)
	types.NewNamed(name, stdBytes, nil)
	pkg.Scope().Insert(name)
	stdFunc(pkg, "Marshal", stdTuple(stdVar(pkg, "v", stdAny)), stdTuple(stdVar(pkg, "", stdBytes), stdVar(pkg, "", stdError)), false)
	stdFunc(pkg, "Unmarshal", stdTuple(stdVar(pkg, "data", stdBytes), stdVar(pkg, "v", stdAny)), stdTuple(stdVar(pkg, "", stdError)), false)
	pkg.MarkComplete()
	return pkg
}
