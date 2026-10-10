// Package pdfjs embeds PDF.js, which previews PDF responses as Yaak's
// PdfViewer does. See UPSTREAM.md.
package pdfjs

import "embed"

// FS holds PDF.js's build, its character maps and standard fonts, and
// react-pdf's text and annotation layer styles.
//
//go:embed build cmaps standard_fonts css
var FS embed.FS
