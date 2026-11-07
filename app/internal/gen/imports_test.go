package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddAppImport(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		changed        bool
		err            string
	}{
		{
			name:    "no imports",
			in:      "{\n  \"name\": \"x\",\n  \"type\": \"module\"\n}\n",
			want:    "{\n  \"imports\": {\n    \"#app/*\": \"./src/_app/*\"\n  },\n  \"name\": \"x\",\n  \"type\": \"module\"\n}\n",
			changed: true,
		},
		{
			name:    "other imports",
			in:      "{\n\t\"name\": \"imports\",\n\t\"imports\": {\n\t\t\"#lib/*\": \"./lib/*\"\n\t}\n}\n",
			want:    "{\n\t\"name\": \"imports\",\n\t\"imports\": {\n\t\t\"#app/*\": \"./src/_app/*\",\n\t\t\"#lib/*\": \"./lib/*\"\n\t}\n}\n",
			changed: true,
		},
		{
			name:    "empty imports",
			in:      "{\n  \"scripts\": { \"imports\": \"x\" },\n  \"imports\": {}\n}\n",
			want:    "{\n  \"scripts\": { \"imports\": \"x\" },\n  \"imports\": {\n    \"#app/*\": \"./src/_app/*\"\n  }\n}\n",
			changed: true,
		},
		{
			name: "present",
			in:   "{\"imports\":{\"#app/*\":\"./src/_app/*\"}}",
			want: "{\"imports\":{\"#app/*\":\"./src/_app/*\"}}",
		},
		{
			name: "another target",
			in:   "{\"imports\":{\"#app/*\":\"./app/*\"}}",
			want: "{\"imports\":{\"#app/*\":\"./app/*\"}}",
			err:  "change it by hand",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, "package.json")
			if err := os.WriteFile(file, []byte(tc.in), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg := Config{Root: root, Out: "src/_app"}
			if !appImportMissing(cfg) && tc.changed {
				t.Fatalf("the entry is reported present")
			}
			changed, err := AddAppImport(cfg)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if changed != tc.changed {
				t.Fatalf("changed = %v", changed)
			}
			got, _ := os.ReadFile(file)
			if string(got) != tc.want {
				t.Fatalf("package.json:\n%s\nwant:\n%s", got, tc.want)
			}
			if tc.err == "" && appImportMissing(cfg) {
				t.Fatalf("the entry is still missing")
			}
		})
	}
}
