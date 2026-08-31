package main

// DOMAcquireMode selects how the client generator obtains a structural DOM
// node. Create is the established CSR behaviour. Hydrate is intentionally an
// internal mode until the generated entry point supplies a checked walker.
type DOMAcquireMode uint8

const (
	DOMAcquireCreate DOMAcquireMode = iota
	DOMAcquireHydrate
)

// DOMAcquireElement is one deterministic structural element acquisition. The
// plan is built during the normal template walk, rather than reconstructed
// from printed JavaScript, so the SSR marker emitter and hydration backend can
// share node order without a second parser or a string protocol.
type DOMAcquireElement struct {
	Variable string
	Tag      string
}

// DOMAcquireTextRange is one dynamic text position bracketed by an SSR
// `v` marker range. Unlike CSR text construction, acquiring it never creates
// or rewrites the initial text node.
type DOMAcquireTextRange struct {
	Variable string
	MarkerID int
}

// DOMAcquireStructuralRange owns an SSR range for a structural block. The
// initial branch stays in place; the range is materialized only after a
// reactive branch change.
type DOMAcquireStructuralRange struct {
	Variable string
	MarkerID int
}

// DOMAcquirePlan records the portion of the component DOM that is acquired as
// live element nodes. Static child runs emitted through innerHTML and dynamic
// structural blocks remain outside this first refactoring seam and will join
// the plan when their marker-aware lowerings are introduced.
type DOMAcquirePlan struct {
	Mode       DOMAcquireMode
	Elements   []DOMAcquireElement
	TextRanges []DOMAcquireTextRange
	Structures []DOMAcquireStructuralRange
}

func (plan *DOMAcquirePlan) addStructuralRange(variable string, markerID int) {
	if plan == nil {
		return
	}
	plan.Structures = append(plan.Structures, DOMAcquireStructuralRange{Variable: variable, MarkerID: markerID})
}

func (plan *DOMAcquirePlan) addTextRange(variable string, markerID int) {
	if plan == nil {
		return
	}
	plan.TextRanges = append(plan.TextRanges, DOMAcquireTextRange{Variable: variable, MarkerID: markerID})
}

func (plan *DOMAcquirePlan) addElement(variable, tag string) int {
	if plan == nil {
		return -1
	}
	id := len(plan.Elements)
	plan.Elements = append(plan.Elements, DOMAcquireElement{Variable: variable, Tag: tag})
	return id
}
