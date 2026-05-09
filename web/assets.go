package webassets

import "embed"

//go:embed templates/*
var Templates embed.FS

//go:embed all:static
var Static embed.FS
