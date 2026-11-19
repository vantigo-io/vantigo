import "./style.css";
import { isDarkScheme, type ScalarGlobal, start } from "./apiref";

declare global {
  interface Window {
    Scalar?: ScalarGlobal;
  }
}

const root = document.getElementById("apiref");
if (root && window.Scalar) {
  let stored: string | null = null;
  try {
    stored = localStorage.getItem("vantigo-color-scheme");
  } catch {
    // Storage is blocked: follow the browser.
  }
  const dark = isDarkScheme(stored, window.matchMedia("(prefers-color-scheme: dark)").matches);
  void start(root, { fetch: window.fetch.bind(window), scalar: window.Scalar, language: navigator.language, dark });
}
