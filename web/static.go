package web

import "embed"

// Files contains the built frontend used by the naked Go deployment.
//
//go:embed dist/*
var Files embed.FS
