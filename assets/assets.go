package assets

import "embed"

//go:generate go run ../internal/scaffold/genhistory -o scaffold-history.json

//go:embed all:vault-scaffold
var Scaffold embed.FS

//go:embed scaffold-history.json
var ScaffoldHistory []byte

//go:embed all:plugin
var Files embed.FS

//go:embed obsidian
var Obsidian embed.FS
