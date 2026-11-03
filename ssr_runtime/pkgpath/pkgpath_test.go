package pkgpath

import (
	"strings"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	for _, tc := range []struct{ name, encoded, pkg string }{
		{"[id]", "id.param", "id_param"},
		{"[[lang]]", "lang.opt", "lang_opt"},
		{"[...rest]", "rest.rest", "rest_rest"},
		{"[id=int]", "id.int.param", "id_int_param"},
		{"(admin)", "admin.group", "admin_group"},
		{"user-list", "user-list", "user_list"},
		{"sitemap.xml", "sitemap.xml", "sitemap_xml"},
		{"users", "users", "users"},
		{"Users2", "Users2", "Users2"},
		{"_private", "_private", "_private"},
		{".hidden", ".hidden", "_hidden"},
		{"2024", "2024", "p2024"},
		{"a~b", "a~b", "a_b"},
		{"go", "go", "go_"},
		{"type", "type", "type_"},
		{"[type]", "type.param", "type_param"},
		{"[slug_2=uuid]", "slug_2.uuid.param", "slug_2_uuid_param"},
	} {
		encoded, err := Encode(tc.name)
		if err != nil || encoded != tc.encoded {
			t.Errorf("Encode(%q) = %q, %v; want %q", tc.name, encoded, err, tc.encoded)
			continue
		}
		if decoded, err := Decode(encoded); err != nil || decoded != tc.name {
			t.Errorf("Decode(%q) = %q, %v; want %q", encoded, decoded, err, tc.name)
		}
		if pkg := PackageName(encoded); pkg != tc.pkg {
			t.Errorf("PackageName(%q) = %q, want %q", encoded, pkg, tc.pkg)
		}
	}
}

func TestEncodeRejects(t *testing.T) {
	for _, tc := range []struct{ name, reason, suggest string }{
		{"[a]-[b]", "mixed segments", "a-b"},
		{"file.[ext]", "mixed segments", "file-ext"},
		{"[[a]", "mixed segments", "a"},
		{"$id", "invalid character", "id"},
		{"a@b", "invalid character", "a-b"},
		{"{x}", "invalid character", "x"},
		{"wow!", "invalid character", "wow"},
		{"a=b", "invalid character", "a-b"},
		{"a+b", "invalid character", "a-b"},
		{"привет", "invalid character", "dir"},
		{"-x", "cannot start with - or ~", "x"},
		{"~x", "cannot start with - or ~", "x"},
		{"x.", "cannot end with .", "x"},
		{"id.param", "reserved suffix", "id"},
		{"a.b.param", "reserved suffix", "a-b"},
		{"x.opt", "reserved suffix", "x"},
		{"x.rest", "reserved suffix", "x"},
		{"x.group", "reserved suffix", "x"},
		{"[user-id]", "parameter name must be an identifier", "[user_id]"},
		{"[1d]", "parameter name must be an identifier", "[d]"},
		{"[id=in-t]", "matcher name must be an identifier", "[id=in_t]"},
		{"[[l-g]]", "optional parameter name", "[[l_g]]"},
		{"[...]", "parameter name must be an identifier", "[name]"},
		{"(a b)", "group name must be an identifier", "(a_b)"},
		{"", "not a directory name", ""},
		{"..", "not a directory name", ""},
	} {
		_, err := Encode(tc.name)
		e, ok := err.(*Error)
		if !ok {
			t.Errorf("Encode(%q): %v, want an *Error", tc.name, err)
			continue
		}
		if !strings.Contains(e.Reason, tc.reason) || e.Suggest != tc.suggest {
			t.Errorf("Encode(%q): reason %q suggest %q; want %q, %q", tc.name, e.Reason, e.Suggest, tc.reason, tc.suggest)
		}
		if e.Suggest != "" {
			if _, err := Encode(e.Suggest); err != nil {
				t.Errorf("Encode(%q): the suggestion %q is not valid: %v", tc.name, e.Suggest, err)
			}
		}
	}
}

func TestDecodeRejects(t *testing.T) {
	for _, encoded := range []string{"a.b.c.param", ".param", "x-y.opt", "[id]", "a.", "1.group"} {
		if decoded, err := Decode(encoded); err == nil {
			t.Errorf("Decode(%q) = %q, want an error", encoded, decoded)
		}
	}
}

func TestPaths(t *testing.T) {
	encoded, err := EncodePath("routes/(admin)/users/[id=int]")
	if err != nil || encoded != "routes/admin.group/users/id.int.param" {
		t.Fatalf("EncodePath = %q, %v", encoded, err)
	}
	if decoded, err := DecodePath(encoded); err != nil || decoded != "routes/(admin)/users/[id=int]" {
		t.Fatalf("DecodePath = %q, %v", decoded, err)
	}
	if _, err := EncodePath("routes/[a]-[b]/x"); err == nil {
		t.Fatal("EncodePath accepted a mixed segment")
	}
	if got, err := EncodePath("."); got != "." || err != nil {
		t.Fatalf("EncodePath(.) = %q, %v", got, err)
	}
}
