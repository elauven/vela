package main

import "strings"

// SSRComponentInstanceIdentity describes one component occurrence in a parent
// template. Namespace is diagnostic metadata only; the cached Path remains the
// authoritative recipe consumed later by Go SSR and hydration lowering.
type SSRComponentInstanceIdentity struct {
	ComponentName string
	TemplateSpan  SectionSpan
	Path          SSRInstancePath
	Namespace     string
	RuntimeScoped bool
}

// BuildSSRComponentInstanceIdentities derives every component occurrence from
// the shared structural lifecycle plan. It never infers identity from a tag
// name alone: two equal component names at different paths remain distinct.
// A #key or #each segment is marked RuntimeScoped because its final namespace
// must include the evaluated key rather than a printed expression string.
func BuildSSRComponentInstanceIdentities(nodes []Node) []SSRComponentInstanceIdentity {
	plan := BuildSSRStructuralLifecyclePlan(nodes)
	var identities []SSRComponentInstanceIdentity
	var visit func(*SSRStructuralLifecyclePlan)
	visit = func(current *SSRStructuralLifecyclePlan) {
		if current == nil {
			return
		}
		if current.Kind == SSRLifecyclePlanComponent {
			component, ok := current.TemplateNode.(*ComponentNode)
			if !ok || component == nil {
				panic("vela: component lifecycle plan is missing source node")
			}
			path := SSRInstancePath{Segments: append([]SSRInstanceSegment(nil), current.InstancePath.Segments...)}
			identities = append(identities, SSRComponentInstanceIdentity{
				ComponentName: component.Name,
				TemplateSpan:  component.Span,
				Path:          path,
				Namespace:     ssrInstancePathNamespace(path),
				RuntimeScoped: ssrInstancePathNeedsRuntimeScope(path),
			})
		}
		for _, child := range current.Children {
			visit(child)
		}
	}
	visit(plan)
	return identities
}

// SSRComponentInstanceIdentityIndex makes structural identity available to
// target lowerers by immutable component source span. A component span is
// unique inside a parsed component and avoids using its display name as an
// identity key.
func SSRComponentInstanceIdentityIndex(nodes []Node) map[SectionSpan]SSRComponentInstanceIdentity {
	identities := BuildSSRComponentInstanceIdentities(nodes)
	index := make(map[SectionSpan]SSRComponentInstanceIdentity, len(identities))
	for _, identity := range identities {
		if _, exists := index[identity.TemplateSpan]; exists {
			panic("vela: duplicate component source span in instance identity index")
		}
		index[identity.TemplateSpan] = identity
	}
	return index
}

// ssrRuntimeComponentSuffix retains only the static path following the final
// runtime #each/#key segment. The backend prefixes it with the evaluated
// runtime scope, making siblings in one row distinct without putting source
// expressions into output IDs.
func ssrRuntimeComponentSuffix(identity SSRComponentInstanceIdentity) string {
	namespace := identity.Namespace
	cut := strings.LastIndex(namespace, "/row")
	if keyCut := strings.LastIndex(namespace, "/key"); keyCut > cut {
		cut = keyCut
		if cut >= 0 {
			cut += len("/key")
		}
	} else if cut >= 0 {
		cut += len("/row")
	}
	if cut < 0 || cut >= len(namespace) {
		panic("vela: runtime component identity has no runtime path suffix")
	}
	suffix := strings.TrimPrefix(namespace[cut:], "/")
	suffix = strings.ReplaceAll(suffix, "/", "--")
	if suffix == "" {
		panic("vela: runtime component identity has empty path suffix")
	}
	return suffix
}

// SSRStaticRepeatedComponentInstanceSuffixes returns the structural suffixes
// required for repeated statically-addressable child component bindings. The
// enclosing component scope remains the prefix at runtime; this helper owns
// only the deterministic compile-time portion. Runtime #each and #key paths
// remain deliberately excluded until their evaluated identity is available.
func SSRStaticRepeatedComponentInstanceSuffixes(nodes []Node) map[SectionSpan]string {
	identities := BuildSSRComponentInstanceIdentities(nodes)
	counts := map[string]int{}
	for _, identity := range identities {
		if !identity.RuntimeScoped {
			counts[identity.ComponentName]++
		}
	}
	suffixes := map[SectionSpan]string{}
	for _, identity := range identities {
		if identity.RuntimeScoped || counts[identity.ComponentName] < 2 {
			continue
		}
		namespace := strings.TrimPrefix(identity.Namespace, "root/")
		namespace = strings.ReplaceAll(namespace, "/", "--")
		if namespace == "" || namespace == "root" {
			panic("vela: repeated component instance has empty structural namespace")
		}
		suffixes[identity.TemplateSpan] = namespace
	}
	return suffixes
}

func ssrInstancePathNeedsRuntimeScope(path SSRInstancePath) bool {
	for _, segment := range path.Segments {
		if segment.Kind == SSRInstanceEachRow || segment.Kind == SSRInstanceKey {
			return true
		}
	}
	return false
}

func ssrInstancePathNamespace(path SSRInstancePath) string {
	namespace := "root"
	for _, segment := range path.Segments {
		switch segment.Kind {
		case SSRInstanceRoot:
			continue
		case SSRInstanceNode:
			namespace += "/n" + itoa(segment.Index)
		case SSRInstanceIfBranch:
			namespace += "/if" + itoa(segment.Index)
		case SSRInstanceKey:
			namespace += "/key"
		case SSRInstanceEachRow:
			namespace += "/row"
		case SSRInstanceAwaitBranch:
			namespace += "/await-" + segment.Name
		case SSRInstanceComponent:
			namespace += "/component-" + sanitizeIdent(segment.Name)
		case SSRInstanceSlot:
			namespace += "/slot-" + sanitizeIdent(segment.Name)
		}
	}
	return namespace
}
