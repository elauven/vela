package main

//go:generate go run ./tools/aliasgen internal/jsast jsast github.com/elauven/vela/internal/jsast jsast_compat.go
//go:generate go run ./tools/aliasgen internal/srcspan srcspan github.com/elauven/vela/internal/srcspan srcspan_compat.go
//go:generate go run ./tools/aliasgen internal/css css github.com/elauven/vela/internal/css css_compat.go
//go:generate go run ./tools/aliasgen internal/diag diag github.com/elauven/vela/internal/diag diag_compat.go
//go:generate go run ./tools/aliasgen internal/sfc sfc github.com/elauven/vela/internal/sfc sfc_compat.go
//go:generate go run ./tools/aliasgen internal/analysis analysis github.com/elauven/vela/internal/analysis analysis_compat.go
