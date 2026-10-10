# PDF.js

Yaak's PDF viewer, which Yeek's PDF preview follows, is react-pdf 11.0.0 on PDF.js. These files are unchanged copies from npm:

- `build/pdf.min.mjs`, `build/pdf.worker.min.mjs`, `cmaps/` and `standard_fonts/` are from `pdfjs-dist` 6.3.289 (`sha512-ZHjSVpDa3D6izMq8/04lvkhkATUmL9px6ChPaXc1k6nU2Mrhlg1/7F0bdUqCwUjw3NsPTfPZsMDUU6ZIcRaeQw==`, the version Yaak's `package-lock.json` pins), under the Apache License 2.0 in `LICENSE`. The standard fonts carry their own licenses (`standard_fonts/LICENSE_FOXIT`, `standard_fonts/LICENSE_LIBERATION`). Yaak ships the same cmaps and standard fonts, without the wasm decoders.
- `css/TextLayer.css` and `css/AnnotationLayer.css` are from `react-pdf` 11.0.0 (MIT, `css/react-pdf-LICENSE`; the files keep PDF.js's Apache notice).

To update, replace them from the versions Yaak's lockfile names.
