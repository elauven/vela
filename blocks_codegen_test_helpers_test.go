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

// legacySSRProtocolTest marks a test of the legacy SSR protocol: value and
// element markers, instance identifiers and the …At APIs, per-component
// payload scripts, and the recipe hydration artifacts. Under the block
// engine the Go renderer writes block markup and a payload stream instead
// (task 040), covered by TestBlocksSSRMarkup*; expression translation is
// shared and stays covered by the legacy run. These tests are removed or
// ported with the legacy engine (task 042).
func legacySSRProtocolTest(t *testing.T) {
	t.Helper()
	if activeDOMEngine == DOMEngineBlocks {
		t.Skip("asserts the legacy SSR markup and hydration protocol; block markup is covered by TestBlocksSSRMarkup*")
	}
}
