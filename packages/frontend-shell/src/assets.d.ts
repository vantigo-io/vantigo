declare module "*.png" {
  const src: string;
  export default src;
}

// The part of Node's fs the token test reads theme.css with (@types/node is not a
// dependency of this package).
declare module "node:fs" {
  export function readFileSync(path: string, encoding: "utf8"): string;
}
