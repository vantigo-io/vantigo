import { relative } from "node:path";
import * as ts from "../../packages/frontend-shell/node_modules/typescript";

const SOURCE_PATTERNS = [
  "packages/frontend-shell/src/**/*.ts",
  "packages/frontend-shell/src/**/*.tsx",
  "apps/*/frontend/src/**/*.ts",
  "apps/*/frontend/src/**/*.tsx",
];

const USER_VISIBLE_PROPS = new Set(["aria-label", "description", "label", "placeholder", "title"]);
const TECHNICAL_PROPS = new Set([
  "action",
  "className",
  "color",
  "component",
  "h",
  "height",
  "icon",
  "href",
  "id",
  "key",
  "method",
  "name",
  "rel",
  "role",
  "size",
  "src",
  "style",
  "target",
  "to",
  "type",
  "value",
  "variant",
  "w",
  "width",
]);
const TECHNICAL_LITERAL_VALUES = new Set(["2FA", "GSRN", "SKU"]);
const TECHNICAL_TEXT_COMPONENTS = new Set(["Code", "Kbd"]);

export type SourceIssueKind = "jsx-text" | "jsx-prop";

export type SourceIssue = {
  filePath: string;
  line: number;
  column: number;
  kind: SourceIssueKind;
  message: string;
};

const normalizePath = (filePath: string): string => filePath.replaceAll("\\", "/");

const isTestFile = (filePath: string): boolean =>
  /(?:^|\/)(?:__tests__|test|tests)(?:\/|$)|\.(?:test|spec)\.[^.]+$/.test(filePath);

const isCatalogFile = (filePath: string): boolean => {
  const normalized = normalizePath(filePath);
  return (
    normalized.includes("/i18n/catalogs/") || normalized.endsWith("/catalog.ts") || normalized.endsWith("/i18n.ts")
  );
};

const isInfrastructureFile = (filePath: string): boolean => {
  const normalized = normalizePath(filePath);
  return (
    normalized.includes("/src/api/") ||
    normalized.includes("/src/test/") ||
    normalized.endsWith("/routeTree.gen.ts") ||
    normalized.endsWith(".d.ts")
  );
};

export const shouldScanProductionSourceFile = (filePath: string): boolean => {
  const normalized = normalizePath(filePath);
  return !isTestFile(normalized) && !isCatalogFile(normalized) && !isInfrastructureFile(normalized);
};

const isTechnicalLiteral = (value: string): boolean => {
  const trimmed = value.trim();
  if (!trimmed) return true;

  // URLs, paths, CSS values, and explicitly known technical identifiers are
  // technical values, not translatable prose. Do not infer this from casing:
  // an all-caps natural-language heading is still user-visible copy.
  return (
    /^(?:https?|mailto|tel):\/\//i.test(trimmed) ||
    /^(?:\/|#|--)/.test(trimmed) ||
    /^(?:var|calc|rgb|rgba|hsl|hsla)\(/i.test(trimmed) ||
    TECHNICAL_LITERAL_VALUES.has(trimmed)
  );
};

const isVisibleText = (value: string): boolean =>
  /\p{L}/u.test(value) && !isTechnicalLiteral(value) && value.trim().length > 1;

const isTechnicalComponentText = (node: ts.Node): boolean => {
  const parent = node.parent;
  return (
    ts.isJsxElement(parent) &&
    ts.isIdentifier(parent.openingElement.tagName) &&
    TECHNICAL_TEXT_COMPONENTS.has(parent.openingElement.tagName.text)
  );
};

const MAX_STATIC_VALUES = 32;

const unwrapExpression = (expression: ts.Expression): ts.Expression => {
  let current = expression;
  while (
    ts.isParenthesizedExpression(current) ||
    ts.isAsExpression(current) ||
    ts.isTypeAssertionExpression(current) ||
    ts.isNonNullExpression(current) ||
    ts.isSatisfiesExpression(current)
  ) {
    current = current.expression;
  }
  return current;
};

const addUnique = (values: string[], value: string): void => {
  if (!values.includes(value) && values.length < MAX_STATIC_VALUES) values.push(value);
};

/** Resolve expressions made entirely from finite, statically known strings. */
const resolveStaticValues = (
  expression: ts.Expression,
  bindings: ReadonlyMap<string, ts.Expression>,
  resolving = new Set<string>(),
): string[] | undefined => {
  const node = unwrapExpression(expression);

  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return [node.text];

  if (ts.isIdentifier(node)) {
    const initializer = bindings.get(node.text);
    if (!initializer || resolving.has(node.text)) return undefined;
    const nextResolving = new Set(resolving).add(node.text);
    return resolveStaticValues(initializer, bindings, nextResolving);
  }

  if (ts.isConditionalExpression(node)) {
    const condition = resolveStaticBoolean(node.condition, bindings, resolving);
    if (condition !== undefined)
      return resolveStaticValues(condition ? node.whenTrue : node.whenFalse, bindings, resolving);

    const whenTrue = resolveStaticValues(node.whenTrue, bindings, resolving);
    const whenFalse = resolveStaticValues(node.whenFalse, bindings, resolving);
    if (!whenTrue || !whenFalse) return undefined;

    const values: string[] = [];
    for (const value of [...whenTrue, ...whenFalse]) addUnique(values, value);
    return values;
  }

  if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.PlusToken) {
    const left = resolveStaticValues(node.left, bindings, resolving);
    const right = resolveStaticValues(node.right, bindings, resolving);
    if (!left || !right) return undefined;

    const values: string[] = [];
    for (const leftValue of left) {
      for (const rightValue of right) addUnique(values, leftValue + rightValue);
    }
    return values;
  }

  if (ts.isTemplateExpression(node)) {
    let values = [node.head.text];
    for (const span of node.templateSpans) {
      const spanValues = resolveStaticValues(span.expression, bindings, resolving);
      if (!spanValues) return undefined;

      const nextValues: string[] = [];
      for (const value of values) {
        for (const spanValue of spanValues) addUnique(nextValues, value + spanValue);
      }
      values = nextValues;
      values = values.map((value) => value + span.literal.text);
    }

    return values;
  }

  return undefined;
};

const resolveStaticBoolean = (
  expression: ts.Expression,
  bindings: ReadonlyMap<string, ts.Expression>,
  resolving: ReadonlySet<string> = new Set<string>(),
): boolean | undefined => {
  const node = unwrapExpression(expression);
  if (node.kind === ts.SyntaxKind.TrueKeyword) return true;
  if (node.kind === ts.SyntaxKind.FalseKeyword) return false;

  if (ts.isIdentifier(node)) {
    const initializer = bindings.get(node.text);
    if (!initializer || resolving.has(node.text)) return undefined;
    return resolveStaticBoolean(initializer, bindings, new Set(resolving).add(node.text));
  }

  if (ts.isPrefixUnaryExpression(node) && node.operator === ts.SyntaxKind.ExclamationToken) {
    const value = resolveStaticBoolean(node.operand, bindings, resolving);
    return value === undefined ? undefined : !value;
  }

  return undefined;
};

const propertyName = (name: ts.PropertyName): string | undefined => {
  if (ts.isIdentifier(name) || ts.isStringLiteral(name) || ts.isNumericLiteral(name)) return name.text;
  return undefined;
};

const collectBindings = (sourceFile: ts.SourceFile): Map<string, ts.Expression> => {
  const bindings = new Map<string, ts.Expression>();
  const visit = (node: ts.Node): void => {
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.initializer) {
      const declarationList = node.parent;
      if (ts.isVariableDeclarationList(declarationList) && (declarationList.flags & ts.NodeFlags.Const) !== 0) {
        bindings.set(node.name.text, node.initializer);
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(sourceFile);
  return bindings;
};

const staticVisibleValues = (
  expression: ts.Expression,
  bindings: ReadonlyMap<string, ts.Expression>,
): string[] | undefined => resolveStaticValues(expression, bindings);

const containsVisibleStaticValue = (
  expression: ts.Expression,
  bindings: ReadonlyMap<string, ts.Expression>,
): boolean => {
  const values = staticVisibleValues(expression, bindings);
  return values?.some(isVisibleText) ?? false;
};

const scanNestedVisibleProps = (
  expression: ts.Expression,
  bindings: ReadonlyMap<string, ts.Expression>,
  onIssue: (node: ts.Node) => void,
  seen = new Set<ts.Node>(),
): void => {
  const node = unwrapExpression(expression);
  if (seen.has(node)) return;
  seen.add(node);

  if (ts.isIdentifier(node)) {
    const initializer = bindings.get(node.text);
    if (initializer) scanNestedVisibleProps(initializer, bindings, onIssue, seen);
    return;
  }

  if (ts.isArrayLiteralExpression(node)) {
    for (const element of node.elements) {
      if (ts.isSpreadElement(element)) continue;
      scanNestedVisibleProps(element, bindings, onIssue, seen);
    }
    return;
  }

  if (ts.isObjectLiteralExpression(node)) {
    for (const property of node.properties) {
      if (ts.isSpreadAssignment(property)) {
        scanNestedVisibleProps(property.expression, bindings, onIssue, seen);
        continue;
      }
      if (ts.isShorthandPropertyAssignment(property)) {
        scanNestedVisibleProps(property.name, bindings, onIssue, seen);
        continue;
      }
      if (!ts.isPropertyAssignment(property)) continue;

      const name = propertyName(property.name);
      if (name && USER_VISIBLE_PROPS.has(name) && containsVisibleStaticValue(property.initializer, bindings)) {
        onIssue(property.initializer);
        continue;
      }

      if (name && TECHNICAL_PROPS.has(name)) continue;

      scanNestedVisibleProps(property.initializer, bindings, onIssue, seen);
    }
    return;
  }

  if (ts.isConditionalExpression(node)) {
    scanNestedVisibleProps(node.whenTrue, bindings, onIssue, seen);
    scanNestedVisibleProps(node.whenFalse, bindings, onIssue, seen);
  }
};

const locationOf = (sourceFile: ts.SourceFile, node: ts.Node): Pick<SourceIssue, "line" | "column"> => {
  const location = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile));
  return { line: location.line + 1, column: location.character + 1 };
};

const addIssue = (
  issues: SourceIssue[],
  sourceFile: ts.SourceFile,
  node: ts.Node,
  kind: SourceIssueKind,
  message: string,
): void => {
  issues.push({ filePath: sourceFile.fileName, kind, message, ...locationOf(sourceFile, node) });
};

/** Find statically rendered JSX copy that is not wrapped in an i18n lookup. */
export const findUntranslatedLiterals = (source: string, filePath: string): SourceIssue[] => {
  const sourceFile = ts.createSourceFile(filePath, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const issues: SourceIssue[] = [];
  const bindings = collectBindings(sourceFile);

  const reportStaticExpression = (node: ts.Expression, issueKind: SourceIssueKind, message: string): void => {
    if (staticVisibleValues(node, bindings)?.some(isVisibleText))
      addIssue(issues, sourceFile, node, issueKind, message);
  };

  const visit = (node: ts.Node): void => {
    if (ts.isJsxSpreadAttribute(node)) {
      scanNestedVisibleProps(node.expression, bindings, (nestedNode) => {
        const nestedName = ts.isPropertyAssignment(nestedNode.parent) ? propertyName(nestedNode.parent.name) : "prop";
        addIssue(
          issues,
          sourceFile,
          nestedNode,
          "jsx-prop",
          `JSX ${nestedName} contains a static user-visible string; use t(...) or a catalog key`,
        );
      });
    }

    if (ts.isJsxAttribute(node)) {
      const name = ts.isIdentifier(node.name) ? node.name.text : undefined;
      const initializer = node.initializer;
      if (name && USER_VISIBLE_PROPS.has(name) && initializer) {
        const message = `JSX ${name} contains a static user-visible string; use t(...) or a catalog key`;
        if (ts.isStringLiteral(initializer)) {
          if (!isTechnicalLiteral(initializer.text)) addIssue(issues, sourceFile, initializer, "jsx-prop", message);
        } else if (ts.isJsxExpression(initializer) && initializer.expression) {
          reportStaticExpression(initializer.expression, "jsx-prop", message);
        }
      }

      if (
        initializer &&
        ts.isJsxExpression(initializer) &&
        initializer.expression &&
        (!name || !TECHNICAL_PROPS.has(name))
      ) {
        scanNestedVisibleProps(initializer.expression, bindings, (nestedNode) => {
          const nestedName = ts.isPropertyAssignment(nestedNode.parent)
            ? propertyName(nestedNode.parent.name)
            : (name ?? "prop");
          addIssue(
            issues,
            sourceFile,
            nestedNode,
            "jsx-prop",
            `JSX ${nestedName} contains a static user-visible string; use t(...) or a catalog key`,
          );
        });
      }
    }

    if (ts.isJsxText(node)) {
      const value = node.text.trim();
      if (isVisibleText(value) && !isTechnicalComponentText(node)) {
        addIssue(
          issues,
          sourceFile,
          node,
          "jsx-text",
          "JSX contains static user-visible text; use t(...) or a catalog key",
        );
      }
    }

    if (ts.isJsxExpression(node) && node.expression) {
      const parent = node.parent;
      const isChildText = ts.isJsxElement(parent) || ts.isJsxFragment(parent);

      if (isChildText) {
        reportStaticExpression(
          node.expression,
          "jsx-text",
          "JSX contains static user-visible text; use t(...) or a catalog key",
        );
      }
    }

    ts.forEachChild(node, visit);
  };

  visit(sourceFile);
  return issues;
};

const scanFiles = async (rootDir: string): Promise<string[]> => {
  const files = new Set<string>();
  for (const pattern of SOURCE_PATTERNS) {
    for await (const filePath of new Bun.Glob(pattern).scan({ cwd: rootDir, absolute: true })) {
      if (shouldScanProductionSourceFile(filePath)) files.add(filePath);
    }
  }
  return [...files].sort();
};

export const validateProductionSource = async (rootDir: string): Promise<SourceIssue[]> => {
  const issues: SourceIssue[] = [];

  for (const filePath of await scanFiles(rootDir)) {
    const relativePath = normalizePath(relative(rootDir, filePath));
    const sourceIssues = findUntranslatedLiterals(await Bun.file(filePath).text(), relativePath);
    issues.push(...sourceIssues);
  }

  return issues.sort(
    (left, right) =>
      left.filePath.localeCompare(right.filePath) || left.line - right.line || left.column - right.column,
  );
};
