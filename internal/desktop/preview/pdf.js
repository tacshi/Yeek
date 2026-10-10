// Yaak's PdfViewer: react-pdf's Document with a Page per page, each as wide
// as the viewer, its text selectable and its links working. This page runs
// in a sandboxed frame, so the PDF and PDF.js reach nothing of Yeek's.
import "./pdfjs/polyfills.js";
import * as pdfjs from "./pdfjs/build/pdf.min.mjs";

// The worker loads the polyfills first: it has its own globals.
pdfjs.GlobalWorkerOptions.workerSrc = new URL("pdfjs/worker.mjs", import.meta.url).href;

const root = document.getElementById("document");

function message(text) {
  root.replaceChildren();
  const div = document.createElement("div");
  div.className = "react-pdf__message";
  div.textContent = text;
  root.append(div);
}

// react-pdf's LinkService, without a pdf_viewer: external links open in a
// new window, which Yeek opens in the browser, and destinations scroll
// their page into view.
const linkService = {
  externalLinkEnabled: true,
  eventBus: { dispatch() {}, on() {}, off() {} },
  pagesCount: 0,
  page: 0,
  rotation: 0,
  isInPresentationMode: false,
  addLinkAttributes(link, url) {
    link.href = url;
    link.rel = "noopener noreferrer";
    link.target = "_blank";
  },
  getDestinationHash() {
    return "#";
  },
  getAnchorUrl() {
    return "#";
  },
  async goToDestination(dest) {
    const explicit = typeof dest === "string" ? await doc.getDestination(dest) : await dest;
    if (!Array.isArray(explicit)) return;
    const ref = explicit[0];
    const index = typeof ref === "number" ? ref : await doc.getPageIndex(ref);
    pages[index]?.scrollIntoView();
  },
  goToPage(number) {
    pages[number - 1]?.scrollIntoView();
  },
  navigateTo(dest) {
    this.goToDestination(dest);
  },
  goToXY() {},
  setHash() {},
  executeNamedAction() {},
  async executeSetOCGState() {},
  cachePageRef() {},
  isPageVisible() {
    return true;
  },
  isPageCached() {
    return true;
  },
};

let doc;
let pages = [];
let width = 0;
let generation = 0;

async function renderPage(page, element, scale, run) {
  // Page and Canvas: drawn at the device's pixel ratio, shown at the scale.
  const viewport = page.getViewport({ scale });
  const ratio = window.devicePixelRatio || 1;
  const canvas = document.createElement("canvas");
  canvas.className = "react-pdf__Page__canvas";
  canvas.width = Math.floor(viewport.width * ratio);
  canvas.height = Math.floor(viewport.height * ratio);
  canvas.style.width = `${Math.floor(viewport.width)}px`;
  canvas.style.height = `${Math.floor(viewport.height)}px`;
  canvas.style.display = "block";
  canvas.style.userSelect = "none";
  const text = document.createElement("div");
  text.className = "react-pdf__Page__textContent textLayer";
  const annotations = document.createElement("div");
  annotations.className = "react-pdf__Page__annotations annotationLayer";
  element.style.setProperty("--user-unit", String(scale));
  element.replaceChildren(canvas, text, annotations);

  await page.render({
    canvas,
    canvasContext: canvas.getContext("2d", { alpha: false }),
    viewport: page.getViewport({ scale: scale * ratio }),
  }).promise;
  if (run !== generation) return;

  const layer = new pdfjs.TextLayer({
    container: text,
    textContentSource: page.streamTextContent({ includeMarkedContent: true }),
    viewport,
  });
  await layer.render();
  const end = document.createElement("div");
  end.className = "endOfContent";
  text.append(end);
  if (run !== generation) return;

  const flipped = viewport.clone({ dontFlip: true });
  await new pdfjs.AnnotationLayer({
    div: annotations,
    page,
    viewport: flipped,
    annotationStorage: doc.annotationStorage,
    linkService,
    accessibilityManager: null,
    annotationCanvasMap: null,
    annotationEditorUIManager: null,
    structTreeLayer: null,
    commentManager: null,
  }).render({
    annotations: await page.getAnnotations(),
    div: annotations,
    page,
    viewport: flipped,
    linkService,
    annotationStorage: doc.annotationStorage,
    renderForms: false,
  });
}

// Each page is as wide as the viewer (react-pdf's width), laid out again
// when the viewer's width changes.
async function renderAll() {
  const run = ++generation;
  for (let i = 0; i < doc.numPages; i++) {
    const page = await doc.getPage(i + 1);
    if (run !== generation) return;
    const scale = width / page.getViewport({ scale: 1 }).width;
    try {
      await renderPage(page, pages[i], scale, run);
    } catch (err) {
      if (run !== generation) return;
      console.error(err);
      pages[i].replaceChildren();
      pages[i].textContent = "Failed to load the page.";
    }
    if (run !== generation) return;
  }
}

async function main() {
  message("Loading PDF…");
  try {
    doc = await pdfjs.getDocument({
      url: "body",
      cMapUrl: new URL("pdfjs/cmaps/", import.meta.url).href,
      cMapPacked: true,
      standardFontDataUrl: new URL("pdfjs/standard_fonts/", import.meta.url).href,
      // The body is one local response: no ranges or streaming.
      disableRange: true,
      disableStream: true,
    }).promise;
  } catch (err) {
    console.error(err);
    message("Failed to load PDF file.");
    return;
  }
  linkService.pagesCount = doc.numPages;
  root.replaceChildren();
  pages = Array.from({ length: doc.numPages }, (_, i) => {
    const element = document.createElement("div");
    element.className = "react-pdf__Page";
    element.dataset.pageNumber = String(i + 1);
    root.append(element);
    return element;
  });
  width = root.clientWidth;
  await renderAll();
  let timer;
  new ResizeObserver(() => {
    const next = root.clientWidth;
    if (next === width || next === 0) return;
    width = next;
    clearTimeout(timer);
    timer = setTimeout(renderAll, 100);
  }).observe(root);
}

main();
