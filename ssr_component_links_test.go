package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSSRLocalComponentLinksRetainsAliasedCanonicalImport(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "pages", "Parent.vela")
	if err := os.MkdirAll(filepath.Dir(parent), 0o755); err != nil {
		t.Fatal(err)
	}
	sfc, err := parseSFC(`<script>import { Card as ProfileCard } from '../ui/Card.vela';</script><template><ProfileCard /></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	links, err := ResolveSSRLocalComponentLinks(parent, root, analysis)
	if err != nil {
		t.Fatal(err)
	}
	link, ok := links["ProfileCard"]
	if !ok || link.SourcePath != filepath.Join(root, "ui", "Card.vela") {
		t.Fatalf("links=%#v", links)
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", sfc, analysis, "vela-parent", links)
	if err != nil {
		t.Fatal(err)
	}
	component := plan.Nodes[0].(*SSRPlanComponent)
	if component.Link == nil || component.Link.LocalName != "ProfileCard" || component.Link.SourcePath != link.SourcePath {
		t.Fatalf("component link=%#v", component.Link)
	}
}

func TestResolveSSRLocalComponentLinksRejectsEscapingImport(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src", "Parent.vela")
	sfc, err := parseSFC(`<script>import { Card } from '../../outside/Card.vela';</script><template><Card /></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveSSRLocalComponentLinks(src, filepath.Join(root, "src"), analyzeScriptAST(sfc.ScriptAST)); err == nil {
		t.Fatal("escaping .vela import was accepted")
	}
}
