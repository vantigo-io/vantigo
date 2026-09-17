import { Anchor, Breadcrumbs, Group, Text, Title } from "@mantine/core";
import type { ReactNode } from "react";
import { useShellLink } from "./link-context";

export interface PageBreadcrumb {
  label: ReactNode;
  /** Where the crumb leads; omit on the last crumb, which is the current page. */
  to?: string;
}

export interface PageHeaderProps {
  /**
   * The trail to this page, ending with the page itself. Detail pages pass
   * the area's list page, then the entity; sidebar destinations pass
   * nothing, since the shell header already names the area.
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
 * The header every page renders: breadcrumbs above on detail pages, the
 * title, an optional description, and right-aligned actions.
 */
export function PageHeader({ breadcrumbs, title, description, actions }: PageHeaderProps) {
  return (
    <Group justify="space-between" align="end">
      <div>
        {breadcrumbs && breadcrumbs.length > 0 && (
          <Breadcrumbs mb={4}>
            {breadcrumbs.map((crumb, index) => (
              <Crumb key={index} {...crumb} />
            ))}
          </Breadcrumbs>
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
