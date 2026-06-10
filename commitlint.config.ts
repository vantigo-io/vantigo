import type { UserConfig } from "@commitlint/types";

const config: UserConfig = {
  extends: ["@commitlint/config-conventional"],
  ignores: [
    (commit) =>
      commit.includes("dependabot") || commit.startsWith("chore(deps):"),
  ],
};

export default config;
