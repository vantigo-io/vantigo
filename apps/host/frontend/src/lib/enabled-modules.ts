import { appConfig } from "@vantigo/frontend-shell";
import { type ModuleKey, moduleKeys } from "../navigation";

/**
 * The enabled module keys: the list the backend injected into the page
 * (its resolved MODULES allowlist) intersected with the modules this build
 * knows, or every known module when nothing was injected, which is the Vite
 * dev server case. Reading is synchronous; the value never changes after load.
 */
export const enabledModuleKeys = (): readonly ModuleKey[] => {
  const injected = appConfig().modules;
  if (injected === undefined) return moduleKeys;
  return moduleKeys.filter((key) => injected.includes(key));
};
