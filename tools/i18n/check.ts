import { resolve } from "node:path";
import { type SourceIssue, validateProductionSource } from "./source-check";
import { validateRepositoryCatalogs } from "./validator";

const rootDir = resolve(import.meta.dir, "../..");

export type I18nCheckResult = {
  catalogs: Awaited<ReturnType<typeof validateRepositoryCatalogs>>;
  sourceIssues: SourceIssue[];
};

export const checkRepository = async (repositoryRoot: string): Promise<I18nCheckResult> => ({
  catalogs: await validateRepositoryCatalogs(repositoryRoot),
  sourceIssues: await validateProductionSource(repositoryRoot),
});

export const runCheck = async (repositoryRoot: string): Promise<number> => {
  if (typeof navigator !== "undefined" && !navigator.language) {
    Object.defineProperty(navigator, "language", { value: "en", configurable: true });
  }
  if (typeof navigator !== "undefined" && !navigator.languages?.length) {
    Object.defineProperty(navigator, "languages", { value: [navigator.language], configurable: true });
  }
  const result = await checkRepository(repositoryRoot);

  if (result.catalogs.catalogs.length === 0) {
    console.error("No i18n catalogs were discovered.");
    return 1;
  }

  if (result.catalogs.issues.length > 0) {
    console.error(`i18n catalog check failed with ${result.catalogs.issues.length} issue(s):`);
    for (const issue of result.catalogs.issues) {
      console.error(`- ${issue.catalog}: ${issue.message}`);
    }
  }

  if (result.sourceIssues.length > 0) {
    console.error(`i18n production source check failed with ${result.sourceIssues.length} issue(s):`);
    for (const issue of result.sourceIssues) {
      console.error(`- ${issue.filePath}:${issue.line}:${issue.column}: ${issue.message}`);
    }
  }

  if (result.catalogs.issues.length > 0 || result.sourceIssues.length > 0) return 1;

  console.log(`i18n checks passed (${result.catalogs.catalogs.length} catalog export(s)).`);
  return 0;
};

if (import.meta.main) {
  try {
    process.exit(await runCheck(rootDir));
  } catch (error) {
    console.error("i18n check could not inspect the repository:");
    console.error(error instanceof Error ? error.message : error);
    process.exit(1);
  }
}
