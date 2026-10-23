package main

//go:generate go run ./tools/aliasgen internal/jsast jsast github.com/elauven/vela/internal/jsast jsast_compat.go
//go:generate go run ./tools/aliasgen internal/srcspan srcspan github.com/elauven/vela/internal/srcspan srcspan_compat.go
//go:generate go run ./tools/aliasgen internal/css css github.com/elauven/vela/internal/css css_compat.go
//go:generate go run ./tools/aliasgen internal/diag diag github.com/elauven/vela/internal/diag diag_compat.go
//go:generate go run ./tools/aliasgen internal/sfcast sfcast github.com/elauven/vela/internal/sfcast sfcast_compat.go
//go:generate go run ./tools/aliasgen internal/analyzer analyzer github.com/elauven/vela/internal/analyzer analyzer_compat.go
//go:generate go run ./tools/aliasgen internal/codegen codegen github.com/elauven/vela/internal/codegen codegen_compat.go
//go:generate go run ./tools/aliasgen internal/project project github.com/elauven/vela/internal/project project_compat.go
//go:generate go run ./tools/aliasgen internal/ssr ssr github.com/elauven/vela/internal/ssr ssr_compat.go
//go:generate go run ./tools/aliasgen internal/compiler compiler github.com/elauven/vela/internal/compiler compiler_compat.go
