package sfcast

import "github.com/elauven/vela/internal/jsast"

// ParseTemplateExpression parses one template expression in statement context
// and preserves parser errors for user-facing diagnostics.
func ParseTemplateExpression(exprText string) (*jsast.JsProgram, []string) {
	if errs := templateExpressionDelimiterErrors(exprText); len(errs) > 0 {
		return nil, errs
	}
	prog, diagnostics := jsast.ParseJSWithDiagnostics("(" + exprText + ");")
	if len(diagnostics) == 0 {
		// Template expressions run inside the component factory.
		diagnostics = jsast.EarlyErrors(prog, jsast.EarlyOptions{TopLevelIsFunction: true})
	}
	var errs []string
	for _, diagnostic := range diagnostics {
		errs = append(errs, diagnostic.Message)
	}
	for _, issue := range jsast.CheckEmptyNodes(prog) {
		errs = append(errs, issue.Msg)
	}
	return prog, errs
}

// ParseTemplateExpressionTS parses a template expression of a TypeScript
// component (TS-4.3) and returns its JavaScript as well: the expression with
// its TypeScript blanked out, of the same length (the source itself when it
// has none or does not parse).
func ParseTemplateExpressionTS(exprText string) (*jsast.JsProgram, []string, string) {
	if errs := templateExpressionDelimiterErrors(exprText); len(errs) > 0 {
		return nil, errs, exprText
	}
	wrapped := "(" + exprText + ");"
	prog, diagnostics := jsast.ParseWithOptions(wrapped, jsast.ParseOptions{TypeScript: true})
	if len(diagnostics) == 0 {
		diagnostics = jsast.EarlyErrors(prog, jsast.EarlyOptions{TopLevelIsFunction: true})
	}
	var errs []string
	for _, diagnostic := range diagnostics {
		errs = append(errs, diagnostic.Message)
	}
	for _, issue := range jsast.CheckEmptyNodes(prog) {
		errs = append(errs, issue.Msg)
	}
	js := exprText
	if len(errs) == 0 && prog != nil && prog.TS != nil && len(prog.TS.Erased) > 0 {
		js = jsast.BlankTypeScript(wrapped, prog.TS)[1 : 1+len(exprText)]
	}
	return prog, errs, js
}

func templateExpressionDelimiterErrors(src string) []string {
	var stack []byte
	var quote byte
	escaped := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == 92 {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == 34 || c == 39 || c == 96 {
			quote = c
			continue
		}
		switch c {
		case 40, 91, 123:
			stack = append(stack, c)
		case 41, 93, 125:
			if len(stack) == 0 {
				return []string{"unexpected closing delimiter"}
			}
			open := stack[len(stack)-1]
			if (open == 40 && c != 41) || (open == 91 && c != 93) || (open == 123 && c != 125) {
				return []string{"mismatched closing delimiter"}
			}
			stack = stack[:len(stack)-1]
		}
	}
	if quote != 0 {
		return []string{"unterminated string literal"}
	}
	if len(stack) > 0 {
		return []string{"unclosed delimiter"}
	}
	return nil
}
