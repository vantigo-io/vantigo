/// <reference path="./assets.d.ts" />
import logo from "./assets/logo.png";
import logoDark from "./assets/logo-dark.png";

/** The shared Vantigo logo displayed in every application shell. */
export const vantigoLogo: string = logo;

/**
 * The same logo for a dark background: the navy stroke lightened to read on the
 * dark theme's surface, the mint kept. Generated from logo.png by
 * scripts/logo-dark.go.
 */
export const vantigoLogoDark: string = logoDark;
