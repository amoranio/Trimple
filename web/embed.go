// Package web embeds the browser UI into the executable.
package web

import "embed"

//go:embed index.html app.js style.css favicon.svg
var Files embed.FS
