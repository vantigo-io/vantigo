import { Title } from "@mantine/core";
import classes from "./auth-page.module.css";

export function AuthTitle({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <Title order={2} className={classes.title}>
      {children}
    </Title>
  );
}
