import type { AssistTarget } from "@vantigo/frontend-shell";
import targets from "./assist-targets.json";

/** The controls the Assistant can point at in a tour; each carries its key as a data-assist attribute. */
export const assistTargets: AssistTarget[] = targets;
