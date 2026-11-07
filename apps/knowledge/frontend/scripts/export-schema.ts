// Writes the editor's ProseMirror schema to src/editor/schema.json (knowledge
// design K8), the file the server's allowlist test reads. Run it with
// `bun run schema:export` after changing the editor's extensions.
import { exportSchema } from "../src/editor/schema";

const file = new URL("../src/editor/schema.json", import.meta.url);
await Bun.write(file, `${JSON.stringify(exportSchema(), null, 2)}\n`);
console.log(`wrote ${file.pathname}`);
