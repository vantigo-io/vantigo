type RuntimeProcess = { env?: { NODE_ENV?: string } };
type RuntimeImportMeta = ImportMeta & { env?: { MODE?: string } };

export const diagnosticsEnabled = (): boolean => {
  const runtimeProcess = (globalThis as typeof globalThis & { process?: RuntimeProcess }).process;
  const nodeMode = runtimeProcess?.env?.NODE_ENV;
  const viteMode = (import.meta as RuntimeImportMeta).env?.MODE;
  return nodeMode !== "production" && viteMode !== "production";
};

export const reportI18nProblem = (message: string): void => {
  if (!diagnosticsEnabled()) return;
  throw new Error(message);
};
