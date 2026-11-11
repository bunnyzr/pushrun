// Package web embeds the built web console assets (web/dist, produced by
// `npm run build` in web/) so the daemon serves the console from the single
// binary at `/`.
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
