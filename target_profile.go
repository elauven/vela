package main

import (
	"fmt"
	"strings"
)

// TargetProfile selects the host globals accepted by the compiler's
// proof-oriented diagnostics. It is deliberately not a type system: only
// direct reads/writes of known globals are checked.
type TargetProfile string

const (
	TargetPermissive TargetProfile = "permissive"
	TargetBrowser    TargetProfile = "browser"
	TargetWorker     TargetProfile = "worker"
	TargetNode       TargetProfile = "node"
	TargetSSR        TargetProfile = "ssr"
)

func (p TargetProfile) String() string {
	if p == "" {
		return string(TargetPermissive)
	}
	return string(p)
}

func (p TargetProfile) normalized() TargetProfile {
	if p == "" {
		return TargetPermissive
	}
	return p
}

// ParseTargetProfile validates a user-facing profile value.
func ParseTargetProfile(value string) (TargetProfile, error) {
	p := TargetProfile(strings.ToLower(strings.TrimSpace(value))).normalized()
	switch p {
	case TargetPermissive, TargetBrowser, TargetWorker, TargetNode, TargetSSR:
		return p, nil
	default:
		return "", fmt.Errorf("invalid target profile %q (want permissive, browser, worker, node, or ssr)", value)
	}
}

// targetExcludedGlobals lists only globals whose absence is guaranteed by a
// target. It intentionally omits debatable or version-dependent APIs, so the
// diagnostic remains sound rather than pretending to perform environment type
// checking. SSR follows Node's server host globals but excludes browser DOM.
func targetExcludedGlobals(profile TargetProfile) map[string]bool {
	excluded := map[string]bool{}
	add := func(names ...string) {
		for _, name := range names {
			excluded[name] = true
		}
	}
	browserDOM := []string{
		"window", "document", "history", "screen", "localStorage", "sessionStorage",
		"indexedDB", "requestAnimationFrame", "cancelAnimationFrame", "alert", "confirm", "prompt",
		"HTMLElement", "Element", "Node", "Text", "DocumentFragment", "FileReader",
		"IntersectionObserver", "ResizeObserver", "MutationObserver", "matchMedia",
		"getComputedStyle", "DOMParser", "Image", "HTMLInputElement", "InputEvent",
		"DragEvent", "PointerEvent", "FocusEvent", "SubmitEvent",
	}
	nodeOnly := []string{"process", "Buffer", "require", "module", "exports", "__dirname", "__filename", "global"}
	switch profile.normalized() {
	case TargetBrowser:
		add(nodeOnly...)
	case TargetWorker:
		add(browserDOM...)
	case TargetNode, TargetSSR:
		add(browserDOM...)
	}
	return excluded
}
