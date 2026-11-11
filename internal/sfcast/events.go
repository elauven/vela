package sfcast

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Event modifiers (task 101): @event.modifier… on DOM element events and on
// <vela:window> / <vela:document> listeners, .once on component events.
// The order of the modifiers is the order of their checks.

// EventModifierKind classifies a modifier.
type EventModifierKind uint8

const (
	// ModBehaviour: prevent, stop, self, trusted.
	ModBehaviour EventModifierKind = iota
	// ModOption: once, capture, passive (once is kept by the wrapper).
	ModOption
	// ModKey: a key of a keyboard event.
	ModKey
	// ModSystem: ctrl, shift, alt, meta.
	ModSystem
	// ModExact: no system key other than the listed ones.
	ModExact
	// ModButton: a mouse button of a mouse or pointer event.
	ModButton
)

// EventModifier is one resolved modifier of an event attribute.
type EventModifier struct {
	Name string
	Kind EventModifierKind
	// Keys are the event.key values a key modifier accepts (compared in
	// lower case for one-character keys); Button is the event.button of a
	// button modifier.
	Keys   []string
	Button int
}

var behaviourModifiers = []string{"prevent", "stop", "self", "trusted"}
var optionModifiers = []string{"once", "capture", "passive"}
var systemModifiers = []string{"ctrl", "shift", "alt", "meta"}

// namedKeys maps key modifiers to event.key values.
var namedKeys = map[string][]string{
	"enter":     {"Enter"},
	"esc":       {"Escape", "Esc"},
	"space":     {" ", "Spacebar"},
	"tab":       {"Tab"},
	"delete":    {"Delete", "Backspace"},
	"up":        {"ArrowUp", "Up"},
	"down":      {"ArrowDown", "Down"},
	"left":      {"ArrowLeft", "Left"},
	"right":     {"ArrowRight", "Right"},
	"page-up":   {"PageUp"},
	"page-down": {"PageDown"},
	"home":      {"Home"},
	"end":       {"End"},
	"insert":    {"Insert"},
}

var mouseButtons = map[string]int{"left": 0, "middle": 1, "right": 2}

// keyboardEvents and mouseEvents are the events key, system key and button
// modifiers apply to.
var keyboardEvents = map[string]bool{"keydown": true, "keyup": true, "keypress": true}
var mouseEvents = map[string]bool{
	"click": true, "dblclick": true, "mousedown": true, "mouseup": true, "mousemove": true,
	"mouseover": true, "mouseout": true, "mouseenter": true, "mouseleave": true,
	"contextmenu": true, "auxclick": true, "wheel": true,
	"pointerdown": true, "pointerup": true, "pointermove": true, "pointerover": true,
	"pointerout": true, "pointerenter": true, "pointerleave": true, "pointercancel": true,
	"drag": true, "dragstart": true, "dragend": true, "dragenter": true, "dragleave": true,
	"dragover": true, "drop": true,
}

// SplitEventName splits "keydown.enter.exact" into the event and its
// modifiers.
func SplitEventName(name string) (string, []string) {
	parts := strings.Split(name, ".")
	return parts[0], parts[1:]
}

// keyModifier resolves a key modifier: a named key, f1–f12, or one letter
// or digit.
func keyModifier(name string) ([]string, bool) {
	if keys, ok := namedKeys[name]; ok {
		return keys, true
	}
	if len(name) >= 2 && name[0] == 'f' {
		if n, err := strconv.Atoi(name[1:]); err == nil && n >= 1 && n <= 12 && name[1] != '0' {
			return []string{"F" + name[1:]}, true
		}
	}
	if len(name) == 1 && (name[0] >= 'a' && name[0] <= 'z' || name[0] >= '0' && name[0] <= '9') {
		return []string{name}, true
	}
	return nil, false
}

func contains(list []string, name string) bool {
	for _, item := range list {
		if item == name {
			return true
		}
	}
	return false
}

// IsKeyboardEvent and IsMouseEvent classify an event name.
func IsKeyboardEvent(event string) bool { return keyboardEvents[event] }
func IsMouseEvent(event string) bool    { return mouseEvents[event] }

// EventModifierError is an invalid modifier: Message and Help for
// VELA-EVENT-MODIFIER.
type EventModifierError struct {
	Message string
	Help    string
}

func (e *EventModifierError) Error() string { return e.Message }

// ResolveEventModifiers checks the modifiers of an event attribute and
// resolves them. component is set for a component event (@on:name), which
// accepts only .once.
func ResolveEventModifiers(event string, modifiers []string, component bool) ([]EventModifier, *EventModifierError) {
	var out []EventModifier
	seen := map[string]bool{}
	for _, name := range modifiers {
		if seen[name] {
			return nil, &EventModifierError{Message: fmt.Sprintf("modifier .%s is given twice on @%s", name, event), Help: "Write ." + name + " once."}
		}
		seen[name] = true
		if component {
			if name != "once" {
				return nil, &EventModifierError{
					Message: fmt.Sprintf("component event @%s accepts only the .once modifier, not .%s", event, name),
					Help:    "Handle ." + name + " inside the handler; component events are not DOM events.",
				}
			}
			out = append(out, EventModifier{Name: name, Kind: ModOption})
			continue
		}
		keyboard, mouse := keyboardEvents[event], mouseEvents[event]
		switch {
		case name == "":
			return nil, &EventModifierError{Message: fmt.Sprintf("@%s has an empty modifier", event), Help: "Remove the extra dot."}
		case contains(behaviourModifiers, name):
			out = append(out, EventModifier{Name: name, Kind: ModBehaviour})
		case contains(optionModifiers, name):
			out = append(out, EventModifier{Name: name, Kind: ModOption})
		case contains(systemModifiers, name):
			if !keyboard && !mouse {
				return nil, &EventModifierError{
					Message: fmt.Sprintf(".%s checks a system key, which only keyboard and mouse events have, not %s", name, event),
					Help:    "Use .ctrl, .shift, .alt and .meta on keydown, keyup, click and other mouse or pointer events.",
				}
			}
			out = append(out, EventModifier{Name: name, Kind: ModSystem})
		case name == "exact":
			if !keyboard && !mouse {
				return nil, &EventModifierError{
					Message: fmt.Sprintf(".exact checks the system keys, which only keyboard and mouse events have, not %s", event),
					Help:    "Use .exact on keydown, keyup, click and other mouse or pointer events.",
				}
			}
			out = append(out, EventModifier{Name: name, Kind: ModExact})
		default:
			if button, ok := mouseButtons[name]; ok && mouse {
				out = append(out, EventModifier{Name: name, Kind: ModButton, Button: button})
				continue
			}
			if keys, ok := keyModifier(name); ok {
				if !keyboard {
					what := "a key"
					if name == "middle" {
						what = "a mouse button"
					}
					return nil, &EventModifierError{
						Message: fmt.Sprintf(".%s checks %s, which %s events do not have; key modifiers apply to keydown, keyup and keypress", name, what, event),
						Help:    "Listen to @keydown." + name + ", or check the event in the handler.",
					}
				}
				out = append(out, EventModifier{Name: name, Kind: ModKey, Keys: keys})
				continue
			}
			if _, ok := mouseButtons[name]; ok {
				return nil, &EventModifierError{
					Message: fmt.Sprintf(".%s checks a mouse button, which %s events do not have", name, event),
					Help:    "Use .left, .middle and .right on click, mousedown, pointerdown and other mouse or pointer events.",
				}
			}
			return nil, &EventModifierError{
				Message: fmt.Sprintf("unknown event modifier .%s on @%s", name, event),
				Help:    unknownModifierHelp(name, event),
			}
		}
	}
	if seen["passive"] && seen["prevent"] {
		return nil, &EventModifierError{
			Message: fmt.Sprintf("@%s cannot be .passive and .prevent: a passive listener cannot prevent the default action", event),
			Help:    "Remove .passive to prevent the default, or .prevent to keep scrolling smooth.",
		}
	}
	return out, nil
}

// validModifiers lists the modifiers an event accepts, for the help.
func validModifiers(event string) []string {
	list := append(append([]string{}, behaviourModifiers...), optionModifiers...)
	if keyboardEvents[event] || mouseEvents[event] {
		list = append(list, systemModifiers...)
		list = append(list, "exact")
	}
	if keyboardEvents[event] {
		keys := make([]string, 0, len(namedKeys))
		for name := range namedKeys {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		list = append(list, keys...)
	} else if mouseEvents[event] {
		list = append(list, "left", "middle", "right")
	}
	return list
}

func unknownModifierHelp(name, event string) string {
	valid := validModifiers(event)
	best, distance := "", 3
	for _, candidate := range valid {
		if d := editDistance(name, candidate); d < distance {
			best, distance = candidate, d
		}
	}
	help := ""
	if best != "" {
		help = "Did you mean ." + best + "? "
	}
	help += "Modifiers of @" + event + ": ." + strings.Join(valid, ", .")
	if keyboardEvents[event] {
		help += ", a letter or digit (.k), .f1–.f12"
	}
	return help + "."
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// EventModifierNames lists the modifiers an event accepts, for completion.
func EventModifierNames(event string, component bool) []string {
	if component {
		return []string{"once"}
	}
	return validModifiers(event)
}
