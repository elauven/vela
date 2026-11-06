package ssr

import (
	"fmt"
	"go/token"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elauven/vela/internal/pkgpath"
	"github.com/elauven/vela/internal/project"
)

// SSRPackagePathDiagnosticCode reports a source directory whose name cannot
// become a Go package directory (task 069, docs/SSR.md "Build").
const SSRPackagePathDiagnosticCode = "VELA-SSR-PACKAGE-PATH"

// ssrGoRelDir encodes a slash-separated directory relative to the source
// root into the Go directory of its generated package ([id] → id.param).
// A name that cannot be encoded is kept; CheckSSRPackagePaths reports it
// before anything is generated.
func ssrGoRelDir(rel string) string {
	encoded, err := pkgpath.EncodePath(rel)
	if err != nil {
		return rel
	}
	return encoded
}

// ssrGoPackageName is the package name of an encoded Go directory: the
// established snake_case stem (id.param → id_param, user-list → user_list),
// with a p prefix before a digit and a _ suffix after a Go keyword.
func ssrGoPackageName(encodedBase string) string {
	name := goFileStem(encodedBase)
	if name[0] >= '0' && name[0] <= '9' {
		name = "p" + name
	}
	if token.IsKeyword(name) {
		name += "_"
	}
	return name
}

// CheckSSRPackagePaths checks the directory of every component: each
// element must encode (pkgpath.Encode), and no two source directories may
// encode to the same Go directory, compared without case. It returns the
// error of every affected file.
func CheckSSRPackagePaths(files []string, srcRoot string) map[string]error {
	out := map[string]error{}
	byEncoded := map[string]string{} // lower-cased Go directory → source directory
	dirs := map[string][]string{}    // source directory → files
	for _, src := range files {
		rel := filepath.ToSlash(componentPackageDir(src, srcRoot))
		dirs[rel] = append(dirs[rel], src)
	}
	names := make([]string, 0, len(dirs))
	for rel := range dirs {
		names = append(names, rel)
	}
	sort.Strings(names)
	for _, rel := range names {
		if rel == "." {
			continue
		}
		encoded, err := pkgpath.EncodePath(rel)
		if err != nil {
			for _, src := range dirs[rel] {
				out[src] = fmt.Errorf("%s: %v (source directory %s)", SSRPackagePathDiagnosticCode, err, rel)
			}
			continue
		}
		key := strings.ToLower(encoded)
		if other, taken := byEncoded[key]; taken {
			err := fmt.Errorf("%s: source directories %s and %s both become the Go package directory %s", SSRPackagePathDiagnosticCode, other, rel, encoded)
			for _, src := range append(append([]string{}, dirs[other]...), dirs[rel]...) {
				out[src] = err
			}
			continue
		}
		byEncoded[key] = rel
	}
	return out
}

// CheckSSRFileNames checks the Go files of every component: X.vela writes
// x.go and x_props.go into its package, in snake_case, so UserCard.vela and
// User-Card.vela, or Card.vela and CardProps.vela, would write one file.
// Names are compared without case. It returns the error of every component
// whose file is already another's, in path order.
func CheckSSRFileNames(files []string, srcRoot string, layout SSRProjectLayout) map[string]error {
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	out := map[string]error{}
	owners := map[string]string{} // lower-cased Go file → its component
	for _, src := range sorted {
		paths := []string{layout.ServerPath(src, srcRoot), layout.PropsPath(src, srcRoot)}
		for _, file := range paths {
			if other, taken := owners[strings.ToLower(file)]; taken && other != src {
				out[src] = fmt.Errorf("%s: %s and %s both write the Go file %s; rename one of them", project.OutputCollisionCode,
					filepath.Base(other), filepath.Base(src), filepath.Base(file))
				break
			}
		}
		if out[src] != nil {
			continue
		}
		for _, file := range paths {
			owners[strings.ToLower(file)] = src
		}
	}
	return out
}
