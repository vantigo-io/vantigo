import "./style.css";
import { isDarkScheme, readStoredScheme, type ScalarGlobal, start } from "./apiref";

declare global {
  interface Window {
    Scalar?: ScalarGlobal;
  }
}

const root = document.getElementById("apiref");
if (root && window.Scalar) {
  const dark = isDarkScheme(readStoredScheme(localStorage), window.matchMedia("(prefers-color-scheme: dark)").matches);
  void start(root, { fetch: window.fetch.bind(window), scalar: window.Scalar, language: navigator.language, dark });
}
