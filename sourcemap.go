package main

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"
)

// SourceMapV3 is the browser source-map interchange format used by Vela.
type SourceMapV3 struct {
	Version        int      `json:"version"`
	File           string   `json:"file"`
	Sources        []string `json:"sources"`
	SourcesContent []string `json:"sourcesContent"`
	Names          []string `json:"names"`
	Mappings       string   `json:"mappings"`
}

// BuildSourceMapV3 converts printer locations into a single-source Source Map
// v3 document. sourcePositions are byte offsets in sourceContent.
func BuildSourceMapV3(file, sourceName, sourceContent string, locations []PrintLocation) SourceMapV3 {
	m := SourceMapV3{Version: 3, File: file, Sources: []string{sourceName}, SourcesContent: []string{sourceContent}, Names: []string{}}
	if len(locations) == 0 {
		return m
	}
	sort.SliceStable(locations, func(i, j int) bool {
		if locations[i].GeneratedLine != locations[j].GeneratedLine {
			return locations[i].GeneratedLine < locations[j].GeneratedLine
		}
		return locations[i].GeneratedColumn < locations[j].GeneratedColumn
	})
	line, col := sourceLineColumn(sourceContent, locations[0].SourcePos)
	prevSourceLine, prevSourceCol := 0, 0
	prevGeneratedLine := 1
	prevGeneratedCol := 0
	emitted := false
	lineHasSegment := false
	var out strings.Builder
	for _, loc := range locations {
		if loc.GeneratedLine < 1 || (emitted && loc.GeneratedLine == prevGeneratedLine && loc.GeneratedColumn == prevGeneratedCol) {
			continue
		}
		for prevGeneratedLine < loc.GeneratedLine {
			out.WriteByte(';')
			prevGeneratedLine++
			prevGeneratedCol = 0
			lineHasSegment = false
		}
		if lineHasSegment {
			out.WriteByte(',')
		}
		line, col = sourceLineColumn(sourceContent, loc.SourcePos)
		out.WriteString(encodeVLQ(loc.GeneratedColumn - prevGeneratedCol))
		out.WriteString(encodeVLQ(0))
		out.WriteString(encodeVLQ(line - prevSourceLine))
		out.WriteString(encodeVLQ(col - prevSourceCol))
		prevGeneratedCol, prevSourceLine, prevSourceCol = loc.GeneratedColumn, line, col
		emitted = true
		lineHasSegment = true
	}
	m.Mappings = out.String()
	return m
}

func SourceMapJSON(m SourceMapV3) ([]byte, error) { return json.Marshal(m) }

func sourceLineColumn(src string, pos int) (line, col int) {
	if pos < 0 {
		pos = 0
	}
	if pos > len(src) {
		pos = len(src)
	}
	for len(src) > 0 && pos > 0 {
		r, size := utf8.DecodeRuneInString(src)
		if size > pos {
			break
		}
		src = src[size:]
		pos -= size
		if r == '\n' {
			line, col = line+1, 0
		} else {
			col++
			if r > 0xffff {
				col++
			}
		}
	}
	return line, col
}

const base64VLQ = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func encodeVLQ(value int) string {
	v := value << 1
	if value < 0 {
		v = (-value << 1) | 1
	}
	var b strings.Builder
	for {
		digit := v & 31
		v >>= 5
		if v != 0 {
			digit |= 32
		}
		b.WriteByte(base64VLQ[digit])
		if v == 0 {
			return b.String()
		}
	}
}
