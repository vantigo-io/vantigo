/**
 * Prefixes root-relative links and asset references in page content with the
 * site's base path. The pages are written with site paths ("/en/user/"), which
 * is what the public site serves; the copy embedded in the server binary is
 * served under /docs, and Astro applies `base` to its own URLs but not to
 * links inside Markdown. Under base "/" this is a no-op.
 */
export function rehypeBaseLinks(base) {
  const prefix = base === "/" ? "" : base.replace(/\/+$/, "");
  const attributes = ["href", "src"];
  const visit = (node) => {
    if (node.type === "element" && node.properties) {
      for (const attribute of attributes) {
        const value = node.properties[attribute];
        if (typeof value === "string" && value.startsWith("/") && !value.startsWith("//") && value !== prefix && !value.startsWith(`${prefix}/`)) {
          node.properties[attribute] = prefix + value;
        }
      }
    }
    for (const child of node.children ?? []) visit(child);
  };
  return () => (tree) => {
    if (prefix) visit(tree);
  };
}
