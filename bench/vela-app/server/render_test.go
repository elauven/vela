package main

import (
	"bytes"
	"io"
	"testing"

	pages "github.com/elauven/vela/bench/vela-app/out/server/components/pages"
)

// BenchmarkRenderBoard renders /render/board?n=200 without HTTP or the
// database: the server-side cost of the Vela renderer alone.
func BenchmarkRenderBoard(b *testing.B) {
	cards, err := loadFixture("../../shared/fixtures/board.json")
	if err != nil {
		b.Fatal(err)
	}
	columns, assignees := groupColumns(cards[:200])
	props := pages.BoardPageProps{Project: fixtureProject, Columns: columns, Assignees: assignees, Live: false}
	b.ReportAllocs()
	for b.Loop() {
		if err := pages.RenderBoardPage(io.Discard, props, pages.BoardPageSlots{}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRenderBoardBuffer renders into a reused bytes.Buffer, as the
// server's page handler does.
func BenchmarkRenderBoardBuffer(b *testing.B) {
	cards, err := loadFixture("../../shared/fixtures/board.json")
	if err != nil {
		b.Fatal(err)
	}
	columns, assignees := groupColumns(cards[:200])
	props := pages.BoardPageProps{Project: fixtureProject, Columns: columns, Assignees: assignees, Live: false}
	var buf bytes.Buffer
	b.ReportAllocs()
	for b.Loop() {
		buf.Reset()
		if err := pages.RenderBoardPage(&buf, props, pages.BoardPageSlots{}); err != nil {
			b.Fatal(err)
		}
	}
}
