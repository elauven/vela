package lsp

import "encoding/json"

// The subset of the Language Server Protocol 3.17 types the server uses.
// Positions are zero-based; characters count UTF-16 code units, the
// default position encoding and the one of Vela diagnostics.

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type InitializeParams struct {
	ProcessID        *int   `json:"processId"`
	RootURI          string `json:"rootUri"`
	RootPath         string `json:"rootPath"`
	WorkspaceFolders []struct {
		URI string `json:"uri"`
	} `json:"workspaceFolders"`
	InitializationOptions struct {
		// DiagnosticsDelay is the debounce of diagnostics after a change, in
		// milliseconds (default 150).
		DiagnosticsDelay *int `json:"diagnosticsDelay"`
		TypeScript       struct {
			// TSDK is a TypeScript with the language service API (5.x or
			// 6.x): its lib directory or package directory.
			TSDK string `json:"tsdk"`
			// Enable false turns the JavaScript features off.
			Enable *bool `json:"enable"`
		} `json:"typescript"`
	} `json:"initializationOptions"`
}

type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type DidChangeTextDocumentParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Version int    `json:"version"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Range *Range `json:"range"`
		Text  string `json:"text"`
	} `json:"contentChanges"`
}

type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type DidSaveTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type DiagnosticSeverity int

const (
	SeverityError       DiagnosticSeverity = 1
	SeverityWarning     DiagnosticSeverity = 2
	SeverityInformation DiagnosticSeverity = 3
	SeverityHint        DiagnosticSeverity = 4
)

type Diagnostic struct {
	Range    Range              `json:"range"`
	Severity DiagnosticSeverity `json:"severity"`
	Code     string             `json:"code,omitempty"`
	Source   string             `json:"source"`
	Message  string             `json:"message"`
}

type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     *int         `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

type CompletionItemKind int

const (
	KindText      CompletionItemKind = 1
	KindMethod    CompletionItemKind = 2
	KindFunction  CompletionItemKind = 3
	KindField     CompletionItemKind = 5
	KindVariable  CompletionItemKind = 6
	KindClass     CompletionItemKind = 7
	KindInterface CompletionItemKind = 8
	KindModule    CompletionItemKind = 9
	KindProperty  CompletionItemKind = 10
	KindKeyword   CompletionItemKind = 14
	KindSnippet   CompletionItemKind = 15
	KindEvent     CompletionItemKind = 23
	KindConstant  CompletionItemKind = 21
	KindTypeParam CompletionItemKind = 25
)

const insertSnippet = 2

type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

type CompletionItem struct {
	Label            string             `json:"label"`
	Kind             CompletionItemKind `json:"kind,omitempty"`
	Detail           string             `json:"detail,omitempty"`
	Documentation    *MarkupContent     `json:"documentation,omitempty"`
	SortText         string             `json:"sortText,omitempty"`
	FilterText       string             `json:"filterText,omitempty"`
	InsertTextFormat int                `json:"insertTextFormat,omitempty"`
	TextEdit         *TextEdit          `json:"textEdit,omitempty"`
	Data             json.RawMessage    `json:"data,omitempty"`
}

type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

type LogMessageParams struct {
	Type    int    `json:"type"`
	Message string `json:"message"`
}

type ReferenceParams struct {
	TextDocumentPositionParams
}

type RenameParams struct {
	TextDocumentPositionParams
	NewName string `json:"newName"`
}
