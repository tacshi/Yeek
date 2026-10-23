package pdfjs

import "embed"

// FS holds PDF.js's build, its character maps and standard fonts, and
// react-pdf's text and annotation layer styles.
//
//go:embed build cmaps standard_fonts css
var FS embed.FS
