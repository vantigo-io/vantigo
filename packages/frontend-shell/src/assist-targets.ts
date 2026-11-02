/**
 * The Assistant's tour targets (design S11). A module frontend that wants the
 * Assistant to be able to point at one of its controls registers the control
 * here and marks it with `data-assist="<key>"`. The model never sees a selector:
 * it chooses a key from the registry, and the tour runner builds the selector.
 */
export interface AssistText {
  en: string;
  nb: string;
}

export interface AssistTarget {
  /** `<module>.<screen>.<control>`, lower case with hyphens: `energy.metering-points.new`. */
  key: string;
  /** The route pattern the control lives on. A `{name}` segment is a record id: `/customers/{customerId}`. */
  route: string;
  /** What the control is called in the interface. */
  label: AssistText;
  /** One line saying what the control is for, for the model and for the documentation. */
  description: AssistText;
}

/** The shape of a key: three lower-case words joined by dots. */
export const assistKeyPattern = /^[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*$/;

/** The CSS selector that finds a registered control. The key is validated first, so it can carry no quote. */
export const assistSelector = (key: string): string => `[data-assist="${assistKeyPattern.test(key) ? key : ""}"]`;

/**
 * Whether a route pattern names a record (`{id}`) and so cannot be navigated to without one:
 * a `{` followed, before the next `}`, by at least one character. A scan rather than a regular
 * expression, so the time stays linear in the route whatever it holds.
 */
export const routeHasParams = (route: string): boolean => {
  let from = 0;
  for (;;) {
    const open = route.indexOf("{", from);
    if (open === -1) return false;
    const close = route.indexOf("}", open + 1);
    if (close === -1) return false;
    if (close > open + 1) return true;
    from = close + 1;
  }
};

/** Whether the current path is the route pattern. A `{name}` segment matches any one segment. */
export const routeMatches = (route: string, pathname: string): boolean => {
  const want = route.split("/").filter(Boolean);
  const have = pathname.split("/").filter(Boolean);
  return want.length === have.length && want.every((part, i) => /^\{[^}]+\}$/.test(part) || part === have[i]);
};

/**
 * The `data-assist` markers in a source file's text. `dynamic` counts markers
 * whose value is not a plain string literal: a registry check cannot see those,
 * so they are not allowed.
 */
export const assistMarkers = (source: string): { keys: string[]; dynamic: number } => {
  const keys: string[] = [];
  let dynamic = 0;
  for (const match of source.matchAll(/data-assist\s*=\s*("([^"]*)"|[^\s>]*)/g)) {
    if (match[2] !== undefined) keys.push(match[2]);
    else dynamic++;
  }
  return { keys, dynamic };
};

/**
 * What is wrong with a package's registry against its own source files
 * (path -> text). Empty when it is right: every registered key is in a marker,
 * every marker is registered, keys are well formed and unique, and routes are
 * paths. Each module package runs this over its `src` in a test, so renaming a
 * control fails CI instead of a tour.
 */
export const assistTargetProblems = (targets: readonly AssistTarget[], sources: Readonly<Record<string, string>>) => {
  const problems: string[] = [];
  const registered = new Set<string>();
  for (const target of targets) {
    if (!assistKeyPattern.test(target.key)) problems.push(`${target.key}: not <module>.<screen>.<control>`);
    if (registered.has(target.key)) problems.push(`${target.key}: registered twice`);
    registered.add(target.key);
    if (!target.route.startsWith("/") || target.route.includes("?"))
      problems.push(`${target.key}: route must be a path`);
    for (const lang of ["en", "nb"] as const) {
      if (!target.label[lang].trim()) problems.push(`${target.key}: label.${lang} is empty`);
      if (!target.description[lang].trim()) problems.push(`${target.key}: description.${lang} is empty`);
    }
  }
  const found = new Set<string>();
  for (const [path, text] of Object.entries(sources)) {
    const { keys, dynamic } = assistMarkers(text);
    if (dynamic > 0) problems.push(`${path}: data-assist must be a string literal`);
    for (const key of keys) {
      found.add(key);
      if (!registered.has(key)) problems.push(`${path}: data-assist="${key}" is not registered`);
    }
  }
  for (const key of registered) if (!found.has(key)) problems.push(`${key}: no data-assist="${key}" in the source`);
  return problems;
};
