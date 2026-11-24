package webdist

import "embed"

// FS embeds the static Next.js export placed in server/webdist.
//
//go:embed all:*
var FS embed.FS
