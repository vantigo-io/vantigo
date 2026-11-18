import "./style.css";
import { type ScalarGlobal, start } from "./apiref";

declare global {
  interface Window {
    Scalar?: ScalarGlobal;
  }
}

const root = document.getElementById("apiref");
if (root && window.Scalar) {
  void start(root, { fetch: window.fetch.bind(window), scalar: window.Scalar, language: navigator.language });
}
