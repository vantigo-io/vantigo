import { Card, type CardProps, Group, type MantineSpacing, Title } from "@mantine/core";
import { type ComponentPropsWithoutRef, type ReactNode, useId } from "react";

export interface PageSectionProps
  extends Omit<CardProps, "title" | "padding" | "withBorder" | "radius">,
    Omit<ComponentPropsWithoutRef<"div">, keyof CardProps | "title"> {
  /** Names the section: rendered as its heading and as its accessible name. */
  title?: ReactNode;
  /** Right-aligned content in the header row (buttons, a menu, badges). */
  actions?: ReactNode;
  /**
   * The surface's inner padding. Pass 0 for a table, grid or calendar that
   * runs edge to edge; the border and radius stay.
   */
  padding?: MantineSpacing | 0;
  /** The element the surface renders as; a titled section is a `section`. */
  component?: "div" | "section" | "article";
  children?: ReactNode;
}

/**
 * The page surface: the white, bordered paper every page's content sits on
 * over the shell's grey background. One per logical block; the PageHeader
 * and KPI rows stay outside it. Colours come from Mantine's tokens, so it
 * follows the colour scheme.
 */
export function PageSection({ title, actions, padding = "lg", component, children, ...rest }: PageSectionProps) {
  const headingId = useId();
  const hasHeader = title !== undefined || actions !== undefined;
  const flush = padding === 0;

  return (
    <Card
      withBorder
      radius="md"
      padding={padding}
      component={component ?? (title !== undefined ? "section" : "div")}
      aria-labelledby={title !== undefined && rest["aria-label"] === undefined ? headingId : undefined}
      {...rest}
    >
      {hasHeader && (
        <Group justify="space-between" align="center" wrap="wrap" gap="sm" mb={flush ? 0 : "md"} p={flush ? "lg" : 0}>
          {title !== undefined ? (
            <Title order={2} size="h4" id={headingId}>
              {title}
            </Title>
          ) : (
            <span />
          )}
          {actions !== undefined && <Group gap="xs">{actions}</Group>}
        </Group>
      )}
      {children}
    </Card>
  );
}
