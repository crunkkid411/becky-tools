package main

// assets.go embeds the WebView2 frontend (HTML/CSS/JS) into the binary so
// becky-clip ships as a single .exe with nothing to install alongside
// (SPEC-BECKY-CLIP §10). server.go serves these over the loopback HTTP server.
//
// The assets/ directory holds:
//   - index.html — the app shell (left search/results, center video+overlay+timeline, right becky)
//   - app.css    — the neon-on-dark theme + layout
//   - app.js     — the frontend logic (beckyCall bridge, render, interactions)

import "embed"

//go:embed assets
var assetsFS embed.FS
