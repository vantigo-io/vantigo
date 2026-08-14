import { relative } from "node:path";
import { pathToFileURL } from "node:url";

const LOCALES = ["en", "nb"] as const;
const CATALOG_EXPORT_NAME = /Catalog$/;
const EXPORTED_CATALOG_PATTERN = /\bexport\s+(?:const|let|var)\s+([A-Za-z_$][\w$]*Catalog)\s*=\s*\{/g;
const INTERPOLATION_PATTERN = /\{\{\s*(-?)\s*([^{}]+?)\s*\}\}/g;

export type CatalogIssueKind =
  | "empty-string"
  | "interpolation-mismatch"
  | "invalid-catalog"
  | "key-mismatch"
  | "type-mismatch";

export type CatalogIssue = {
  catalog: string;
  kind: CatalogIssueKind;
  message: string;
};

export type DiscoveredCatalog = {
  exportName: string;
  filePath: string;
  resources: unknown;
};

export type ValidationResult = {
  catalogs: DiscoveredCatalog[];
  issues: CatalogIssue[];
};

type TranslationObject = Record<string, unknown>;

const isTranslationObject = (value: unknown): value is TranslationObject =>
  typeof value === "object" && value !== null && !Array.isArray(value);

const displayPath = (path: string): string => path || "<root>";

const relativeCatalogPath = (filePath: string, rootDir: string): string => {
  const path = relative(rootDir, filePath);
  return path.startsWith("../") ? filePath : path;
};

const addIssue = (issues: CatalogIssue[], catalog: string, kind: CatalogIssueKind, message: string): void => {
  issues.push({ catalog, kind, message });
};

const interpolationNames = (value: string): string[] => {
  const names = new Set<string>();

  for (const match of value.matchAll(INTERPOLATION_PATTERN)) {
    const name = match[2].trim();
    if (name) names.add(name);
  }

  return [...names].sort();
};

const validateValueShape = (
  value: unknown,
  locale: string,
  path: string,
  catalog: string,
  issues: CatalogIssue[],
): void => {
  if (typeof value === "string") {
    if (value.trim().length === 0) {
      addIssue(issues, catalog, "empty-string", `${locale}.${displayPath(path)} is an empty translation string`);
    }
    return;
  }

  if (isTranslationObject(value)) {
    for (const [key, child] of Object.entries(value)) {
      validateValueShape(child, locale, path ? `${path}.${key}` : key, catalog, issues);
    }
    return;
  }

  addIssue(issues, catalog, "invalid-catalog", `${locale}.${displayPath(path)} must be a string or nested object`);
};

const compareValues = (en: unknown, nb: unknown, path: string, catalog: string, issues: CatalogIssue[]): void => {
  const enIsObject = isTranslationObject(en);
  const nbIsObject = isTranslationObject(nb);

  if (enIsObject && nbIsObject) {
    const keys = new Set([...Object.keys(en), ...Object.keys(nb)]);

    for (const key of keys) {
      const childPath = path ? `${path}.${key}` : key;
      const hasEn = Object.hasOwn(en, key);
      const hasNb = Object.hasOwn(nb, key);

      if (!hasEn) {
        addIssue(issues, catalog, "key-mismatch", `Missing key in en: ${childPath}`);
        continue;
      }
      if (!hasNb) {
        addIssue(issues, catalog, "key-mismatch", `Missing key in nb: ${childPath}`);
        continue;
      }

      compareValues(en[key], nb[key], childPath, catalog, issues);
    }
    return;
  }

  if (enIsObject !== nbIsObject || typeof en !== typeof nb) {
    addIssue(issues, catalog, "type-mismatch", `Value type differs between en and nb at ${displayPath(path)}`);
    return;
  }

  if (typeof en === "string" && typeof nb === "string") {
    const enNames = interpolationNames(en);
    const nbNames = interpolationNames(nb);

    if (enNames.join("\u0000") !== nbNames.join("\u0000")) {
      addIssue(
        issues,
        catalog,
        "interpolation-mismatch",
        `Interpolation names differ at ${displayPath(path)} (en: ${enNames.join(", ") || "none"}; nb: ${nbNames.join(", ") || "none"})`,
      );
    }
  }
};

/** Validate one exported `{ en: ..., nb: ... }` catalog. */
export const validateCatalog = (catalog: string, resources: unknown): CatalogIssue[] => {
  const issues: CatalogIssue[] = [];

  if (!isTranslationObject(resources)) {
    addIssue(issues, catalog, "invalid-catalog", "Catalog export must be an object");
    return issues;
  }

  for (const locale of LOCALES) {
    const value = resources[locale];
    if (!Object.hasOwn(resources, locale)) {
      addIssue(issues, catalog, "invalid-catalog", `Catalog is missing the ${locale} locale`);
    } else if (!isTranslationObject(value)) {
      addIssue(issues, catalog, "invalid-catalog", `Catalog locale ${locale} must be an object`);
    } else {
      validateValueShape(value, locale, "", catalog, issues);
    }
  }

  const en = resources.en;
  const nb = resources.nb;
  if (isTranslationObject(en) && isTranslationObject(nb)) {
    compareValues(en, nb, "", catalog, issues);
  }

  return issues;
};

const scanFiles = async (rootDir: string): Promise<string[]> => {
  const files = new Set<string>();
  const patterns = [
    "packages/frontend-shell/src/i18n/catalogs/**/*.ts",
    "packages/frontend-shell/src/i18n/catalogs/**/*.tsx",
    "apps/*/frontend/src/**/*.ts",
    "apps/*/frontend/src/**/*.tsx",
  ];

  for (const pattern of patterns) {
    for await (const filePath of new Bun.Glob(pattern).scan({ cwd: rootDir, absolute: true })) {
      files.add(filePath);
    }
  }

  return [...files].sort();
};

/** Discover named `*Catalog` exports without importing unrelated application modules. */
export const discoverCatalogs = async (rootDir: string): Promise<DiscoveredCatalog[]> => {
  const catalogs: DiscoveredCatalog[] = [];

  for (const filePath of await scanFiles(rootDir)) {
    const source = await Bun.file(filePath).text();
    const exportNames = [...source.matchAll(EXPORTED_CATALOG_PATTERN)].map((match) => match[1]);
    if (exportNames.length === 0) continue;

    const module = await import(pathToFileURL(filePath).href);
    for (const exportName of exportNames) {
      const resources = module[exportName];
      if (CATALOG_EXPORT_NAME.test(exportName) && isTranslationObject(resources)) {
        catalogs.push({ exportName, filePath, resources });
      }
    }
  }

  return catalogs.sort((left, right) => {
    const fileOrder = left.filePath.localeCompare(right.filePath);
    return fileOrder || left.exportName.localeCompare(right.exportName);
  });
};

export const validateDiscoveredCatalogs = (catalogs: DiscoveredCatalog[], rootDir: string): CatalogIssue[] => {
  const issues: CatalogIssue[] = [];

  for (const catalog of catalogs) {
    const name = `${relativeCatalogPath(catalog.filePath, rootDir)}#${catalog.exportName}`;
    issues.push(...validateCatalog(name, catalog.resources));
  }

  return issues;
};

export const validateRepositoryCatalogs = async (rootDir: string): Promise<ValidationResult> => {
  const catalogs = await discoverCatalogs(rootDir);
  return { catalogs, issues: validateDiscoveredCatalogs(catalogs, rootDir) };
};
