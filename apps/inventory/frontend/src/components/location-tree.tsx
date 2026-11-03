import { ActionIcon, Anchor, Badge, Box, Group, Menu, Stack, Text, TextInput } from "@mantine/core";
import {
  IconArchive,
  IconChevronDown,
  IconChevronRight,
  IconDots,
  IconGridDots,
  IconPencil,
  IconPlus,
  IconRestore,
  IconSearch,
  IconTrash,
} from "@tabler/icons-react";
import { Link } from "@tanstack/react-router";
import { EmptyState } from "@vantigo/frontend-shell";
import { Fragment, type ReactNode, useMemo, useState } from "react";
import type { InventoryLocation } from "../api/locations";
import { useInventoryFormat } from "../lib/format";
import { locationLinkOptions } from "../lib/routes";

/** Below this many nodes the tree filters as the person types; above it, on Enter. */
const LIVE_SEARCH_LIMIT = 2000;

/** What a manager may do to a node, offered from its menu. */
export interface LocationTreeActions {
  onAddChild: (location: InventoryLocation) => void;
  onBulkAdd: (location: InventoryLocation) => void;
  onEdit: (location: InventoryLocation) => void;
  onArchive: (location: InventoryLocation) => void;
  onReactivate: (location: InventoryLocation) => void;
  onDelete: (location: InventoryLocation) => void;
}

export interface LocationTreeProps {
  /** The site's locations, flat, as `GET /sites/{id}/locations` answers them. */
  locations: readonly InventoryLocation[];
  /** The menu's actions; no menu without them (`locations-manage`). */
  actions?: LocationTreeActions;
}

/**
 * A site's location tree (I2), drawn a level at a time: only an expanded
 * node's children are rendered, so a site of thousands of shelves opens as
 * fast as one of ten. The root starts expanded. Searching by code or name
 * lists the matching locations by full code instead.
 */
export const LocationTree = ({ locations, actions }: LocationTreeProps) => {
  const { t } = useInventoryFormat();
  const children = useMemo(() => {
    const map = new Map<number | undefined, InventoryLocation[]>();
    for (const location of locations) {
      const list = map.get(location.parentId) ?? [];
      list.push(location);
      map.set(location.parentId, list);
    }
    for (const list of map.values())
      list.sort((a, b) => a.sortOrder - b.sortOrder || a.code.localeCompare(b.code, undefined, { numeric: true }));
    return map;
  }, [locations]);
  const roots = children.get(undefined) ?? [];
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(() => new Set(roots.map((root) => root.id)));
  const [typed, setTyped] = useState("");
  const [committed, setCommitted] = useState("");
  const live = locations.length < LIVE_SEARCH_LIMIT;
  const query = (live ? typed : committed).trim().toLowerCase();
  const matches = query
    ? locations.filter(
        (location) =>
          location.fullCode.toLowerCase().includes(query) || (location.name ?? "").toLowerCase().includes(query),
      )
    : [];

  const toggle = (id: number) => {
    const next = new Set(expanded);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setExpanded(next);
  };

  // A function rather than a component, so a row is not remounted on every render of the tree.
  const row = (location: InventoryLocation, depth: number, flat = false): ReactNode => {
    const below = children.get(location.id) ?? [];
    const open = expanded.has(location.id);
    return (
      <Fragment key={location.id}>
        <Group
          gap="xs"
          wrap="nowrap"
          pl={flat ? 0 : depth * 20}
          py={4}
          style={{ borderBottom: "1px solid var(--mantine-color-default-border)" }}
        >
          <Box w={28}>
            {!flat && below.length > 0 && (
              <ActionIcon
                variant="subtle"
                size="sm"
                aria-label={t(open ? "collapse" : "expand", { code: location.fullCode })}
                aria-expanded={open}
                onClick={() => toggle(location.id)}
              >
                {open ? <IconChevronDown size={16} /> : <IconChevronRight size={16} />}
              </ActionIcon>
            )}
          </Box>
          <Anchor
            size="sm"
            ff="monospace"
            renderRoot={(props) => <Link {...locationLinkOptions(location.id)} {...props} />}
          >
            {flat ? location.fullCode : location.code}
          </Anchor>
          {location.name && (
            <Text size="sm" c="dimmed" truncate>
              {location.name}
            </Text>
          )}
          <Badge size="sm" variant="light" color="gray">
            {t(`locationKind_${location.kind}`)}
          </Badge>
          {location.status === "archived" && (
            <Badge size="sm" variant="light" color="yellow">
              {t("status_archived")}
            </Badge>
          )}
          {actions && (
            <Menu position="bottom-end" withinPortal>
              <Menu.Target>
                <ActionIcon variant="subtle" ml="auto" aria-label={t("locationActions", { code: location.fullCode })}>
                  <IconDots size={16} />
                </ActionIcon>
              </Menu.Target>
              <Menu.Dropdown>
                {location.status === "active" && (
                  <Menu.Item leftSection={<IconPlus size={14} />} onClick={() => actions.onAddChild(location)}>
                    {t("addChild")}
                  </Menu.Item>
                )}
                {location.status === "active" && (
                  <Menu.Item leftSection={<IconGridDots size={14} />} onClick={() => actions.onBulkAdd(location)}>
                    {t("bulkAdd")}
                  </Menu.Item>
                )}
                <Menu.Item leftSection={<IconPencil size={14} />} onClick={() => actions.onEdit(location)}>
                  {t("edit")}
                </Menu.Item>
                {location.kind !== "site" &&
                  (location.status === "active" ? (
                    <Menu.Item leftSection={<IconArchive size={14} />} onClick={() => actions.onArchive(location)}>
                      {t("archive")}
                    </Menu.Item>
                  ) : (
                    <Menu.Item leftSection={<IconRestore size={14} />} onClick={() => actions.onReactivate(location)}>
                      {t("reactivate")}
                    </Menu.Item>
                  ))}
                {location.kind !== "site" && (
                  <Menu.Item
                    color="red"
                    leftSection={<IconTrash size={14} />}
                    onClick={() => actions.onDelete(location)}
                  >
                    {t("delete")}
                  </Menu.Item>
                )}
              </Menu.Dropdown>
            </Menu>
          )}
        </Group>
        {!flat && open && below.map((child) => row(child, depth + 1))}
      </Fragment>
    );
  };

  return (
    <Stack gap="sm">
      <TextInput
        leftSection={<IconSearch size={16} />}
        aria-label={t("searchLocations")}
        placeholder={t(live ? "searchLocations" : "searchLocationsEnter")}
        value={typed}
        onChange={(event) => {
          setTyped(event.currentTarget.value);
          if (event.currentTarget.value === "") setCommitted("");
        }}
        onKeyDown={(event) => {
          if (event.key === "Enter") setCommitted(typed);
        }}
      />
      {query ? (
        matches.length === 0 ? (
          <EmptyState size="sm" title={t("noLocationsFound")} />
        ) : (
          <Box>{matches.map((location) => row(location, 0, true))}</Box>
        )
      ) : (
        <Box>{roots.map((root) => row(root, 0))}</Box>
      )}
    </Stack>
  );
};
