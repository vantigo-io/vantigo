import { Tabs } from "@mantine/core";
import type { ComponentType, ReactNode } from "react";

export interface PageTab<Value extends string> {
  value: Value;
  label: ReactNode;
  icon?: ComponentType<{ size?: number }>;
}

export interface PageTabsProps<Value extends string> {
  items: readonly PageTab<Value>[];
  /** The selected view; the caller derives it from the URL (a child route or a search param). */
  value: Value;
  /** Called with the chosen view; the caller navigates. */
  onChange: (value: Value) => void;
  "aria-label"?: string;
  /** Optional `Tabs.Panel`s when the views are rendered in place rather than through an outlet. */
  children?: ReactNode;
}

/**
 * The tab row every page with several views renders, directly under its
 * `PageHeader`. Tabs are views of one page and always live in the URL, so
 * the row is controlled: the caller reads the URL and navigates on change.
 * Pages within an area are sidebar destinations, not tabs.
 */
export function PageTabs<Value extends string>({
  items,
  value,
  onChange,
  "aria-label": ariaLabel,
  children,
}: PageTabsProps<Value>) {
  return (
    <Tabs value={value} onChange={(next) => next !== null && onChange(next as Value)} keepMounted={false}>
      <Tabs.List aria-label={ariaLabel}>
        {items.map(({ value: itemValue, label, icon: Icon }) => (
          <Tabs.Tab key={itemValue} value={itemValue} leftSection={Icon ? <Icon size={16} /> : undefined}>
            {label}
          </Tabs.Tab>
        ))}
      </Tabs.List>
      {children}
    </Tabs>
  );
}
