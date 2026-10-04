package main

import "testing"

// legacyCodeShapeTest marks a test that asserts the generated code text of
// the legacy DOM engine (variable names, emitted DOM calls). Its behaviour is
// covered by the runtime suites, which run under both engines; under the
// block engine the text assertions do not apply (Q-10.1) and the test is
// skipped. These tests are removed with the legacy engine (task 042).
func legacyCodeShapeTest(t *testing.T) {
	t.Helper()
	if activeDOMEngine == DOMEngineBlocks {
		t.Skip("asserts legacy generated code text; behaviour is covered by the runtime suites")
	}
}
