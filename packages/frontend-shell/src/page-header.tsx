import { Anchor, Breadcrumbs, Group, Text, Title } from "@mantine/core";
import type { CSSProperties, ReactNode } from "react";
import { useShellLink } from "./link-context";

const eyebrowStyle: CSSProperties = {
  fontSize: 11,
  fontWeight: 700,
  letterSpacing: "0.12em",
  textTransform: "uppercase",
  color: "var(--mantine-color-vantigo-5)",
};

export interface PageBreadcrumb {
  label: ReactNode;
  /** Where the crumb leads; omit on the last crumb, which is the current page. */
  to?: string;
}

export interface PageHeaderProps {
  /**
   * The area the page belongs to, e.g. "Communications". For top-level pages
   * (sidebar destinations); detail pages pass `breadcrumbs` instead, never both.
   */
  eyebrow?: string;
  /**
   * The trail to this page, ending with the page itself. For detail pages:
   * the area's list page, then the entity.
   */
  breadcrumbs?: readonly PageBreadcrumb[];
  /** Page name, ideally matching the navigation label. Text plus optional badges; no icons. */
  title: ReactNode;
  /** One or two lines helping a first-time user understand the page */
  description?: ReactNode;
  /** Optional right-aligned content (badges, action buttons, ...) */
  actions?: ReactNode;
}

const Crumb = ({ label, to }: PageBreadcrumb) => {
  const Link = useShellLink();
  if (to === undefined)
    return (
      <Text size="sm" component="span">
        {label}
      </Text>
    );
  if (Link)
    return (
      // Mantine's polymorphic `component` prop cannot be typed against an
      // arbitrary link component; renderRoot takes the same props and lets
      // the host's router link render the anchor.
      <Anchor size="sm" renderRoot={(props) => <Link to={to} {...props} />}>
        {label}
      </Anchor>
    );
  return (
    <Anchor href={to} size="sm">
      {label}
    </Anchor>
  );
};

/**
 * The header every page renders: context above (eyebrow or breadcrumbs),
 * the title, an optional description, and right-aligned actions.
 */
export function PageHeader({ eyebrow, breadcrumbs, title, description, actions }: PageHeaderProps) {
  return (
    <Group justify="space-between" align="end">
      <div>
        {breadcrumbs && breadcrumbs.length > 0 ? (
          <Breadcrumbs mb={4}>
            {breadcrumbs.map((crumb, index) => (
              <Crumb key={index} {...crumb} />
            ))}
          </Breadcrumbs>
        ) : (
          eyebrow && <Text style={eyebrowStyle}>{eyebrow}</Text>
        )}
        <Title order={2}>{title}</Title>
        {description && (
          <Text c="dimmed" component="div">
            {description}
          </Text>
        )}
      </div>
      {actions}
    </Group>
  );
}
