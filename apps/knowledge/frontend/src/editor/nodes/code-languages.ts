/**
 * The languages a code block can be set to (knowledge design K18), each a
 * short lowercase id as it is stored in the block's `language` and its
 * name as the chooser shows it. Highlighting comes from lowlight's common set.
 */
export const codeLanguages: readonly { id: string; name: string }[] = [
  { id: "bash", name: "Bash" },
  { id: "c", name: "C" },
  { id: "cpp", name: "C++" },
  { id: "csharp", name: "C#" },
  { id: "css", name: "CSS" },
  { id: "diff", name: "Diff" },
  { id: "go", name: "Go" },
  { id: "graphql", name: "GraphQL" },
  { id: "xml", name: "HTML / XML" },
  { id: "ini", name: "INI" },
  { id: "java", name: "Java" },
  { id: "javascript", name: "JavaScript" },
  { id: "json", name: "JSON" },
  { id: "kotlin", name: "Kotlin" },
  { id: "markdown", name: "Markdown" },
  { id: "php", name: "PHP" },
  { id: "python", name: "Python" },
  { id: "ruby", name: "Ruby" },
  { id: "rust", name: "Rust" },
  { id: "scss", name: "SCSS" },
  { id: "shell", name: "Shell" },
  { id: "sql", name: "SQL" },
  { id: "swift", name: "Swift" },
  { id: "typescript", name: "TypeScript" },
  { id: "yaml", name: "YAML" },
];
