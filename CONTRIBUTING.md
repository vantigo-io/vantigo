# 🤝 Contributing to Vantigo

Thank you for considering a contribution! This guide covers everything you need to get a
change from idea to merged pull request.

## 🚀 Getting your environment running

1. Install [Docker](https://www.docker.com/) and [mise](https://mise.jdx.dev/)
2. Fork and clone the repository, then:

```bash
cd vantigo
mise trust && mise install     # Node.js, pnpm and env management
cp .env.example .env           # local configuration
docker compose up -d           # PostgreSQL
pnpm install
pnpm --filter @vantigo/customers db migrate
pnpm dev
```

## 🔀 Workflow

1. **Open or find an issue** describing the bug/feature — for larger changes, please discuss
   before investing significant time
2. **Create a branch** from `main`
3. **Make your change**, including tests and translations (`messages/en.json` + `nb.json`)
4. **Verify locally** — everything CI runs:

   ```bash
   pnpm lint                                     # Biome
   pnpm build                                    # production build
   pnpm test                                     # unit tests
   pnpm --filter @vantigo/customers test:e2e     # end-to-end (needs Docker)
   ```

5. **Open a pull request** — CI must be green before review

## 📝 Commit conventions

We use [Conventional Commits](https://www.conventionalcommits.org/), enforced by commitlint:

```
feat(customers): add customer export to CSV
fix(customers): correct mod11 validation for edge case
ci: cache playwright browsers
docs: clarify container env variables
```

The commit history drives both semver computation and release notes, so well-formed commits
matter. Use `feat` for user-facing features, `fix` for bug fixes; breaking changes get a `!`
(e.g. `feat!:`) or a `BREAKING CHANGE:` footer.

## 🧭 Code guidelines

- **TypeScript strict** — no `any` unless genuinely unavoidable (and justified with a comment)
- **Validation with zod** — shared schemas in `lib/`, used by both API routes and forms
- **i18n everything** — no hardcoded user-facing strings; add keys to both `en.json` and `nb.json`
- **Tests accompany changes** — pure logic gets unit tests; user flows get e2e coverage
- **Formatting/linting is Biome** — `pnpm exec biome check --write .` fixes most issues

## 🔒 Security

Found a security issue? Please **do not open a public issue** — email the maintainer instead
(see repository owner profile) so it can be fixed before disclosure.

## 📄 License

By contributing you agree that your contributions are licensed under the
[AGPL-3.0](LICENSE), the same license as the project.
