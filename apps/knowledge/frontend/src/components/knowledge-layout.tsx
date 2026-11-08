import { Anchor, Box, Flex, Group, ScrollArea } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { IconTrash } from "@tabler/icons-react";
import { Link } from "@tanstack/react-router";
import { PageSection, useI18n } from "@vantigo/frontend-shell";
import { type ReactNode, useState } from "react";
import { TRASH_ROUTE_PATH } from "../lib/routes";
import { SEARCH_DEBOUNCE_MS, SearchResults, SidebarSearchBox } from "./sidebar-search";
import { SidebarTree } from "./sidebar-tree";

/**
 * Every Knowledge page: the sidebar (search above the tree, the trash below
 * it) beside the page's own content (knowledge design K4). On a narrow
 * screen the sidebar sits above the content.
 */
export const KnowledgeLayout = ({ activeId, children }: { activeId?: string; children: ReactNode }) => {
  const { t } = useI18n("knowledge");
  const [query, setQuery] = useState("");
  const [debounced] = useDebouncedValue(query.trim(), SEARCH_DEBOUNCE_MS);
  return (
    <Flex gap="md" align="flex-start" direction={{ base: "column", md: "row" }}>
      <PageSection
        padding="sm"
        component="div"
        aria-label={t("sidebarLabel")}
        w={{ base: "100%", md: 280 }}
        style={{ flex: "none", position: "sticky", top: "var(--mantine-spacing-md)" }}
      >
        <SidebarSearchBox value={query} onChange={setQuery} />
        {/* Vertical only, and the content a block rather than Mantine's table:
            a table grows to its widest row, so one long title would widen the
            tree and scroll the star and + out of sight. */}
        <ScrollArea.Autosize
          mah="calc(100vh - 220px)"
          mt="sm"
          type="auto"
          scrollbars="y"
          styles={{ content: { display: "block", minWidth: 0 } }}
        >
          {query.trim() ? (
            <SearchResults query={debounced} onOpen={() => setQuery("")} />
          ) : (
            <SidebarTree activeId={activeId} />
          )}
        </ScrollArea.Autosize>
        <Group mt="sm" px="xs">
          <Anchor
            size="sm"
            c="dimmed"
            renderRoot={(props) => <Link to={TRASH_ROUTE_PATH} {...props} />}
            style={{ display: "flex", alignItems: "center", gap: 6 }}
          >
            <IconTrash size={14} />
            {t("trashTitle")}
          </Anchor>
        </Group>
      </PageSection>
      <Box style={{ flex: 1, minWidth: 0 }} w="100%">
        {children}
      </Box>
    </Flex>
  );
};
