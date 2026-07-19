import { Group, Paper } from "@mantine/core";
import { LanguageToggle } from "@vantigo/customers/lib/i18n/language-toggle";
import classes from "./auth-page.module.css";

/** Shared shell for all unauthenticated pages: background, panel and language toggle. */
export default function AuthLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <div className={classes.wrapper}>
      <Paper className={classes.form}>
        <Group justify="flex-end">
          <LanguageToggle />
        </Group>

        {children}
      </Paper>
    </div>
  );
}
