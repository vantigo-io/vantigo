import { Badge, Card, Center, Group, Stack, Text, Title, UnstyledButton } from "@mantine/core";
import { IconBuildingSkyscraper, IconChevronRight } from "@tabler/icons-react";
import { Link } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import "../i18n";
import type { Tenant } from "../api/auth";

interface TenantSelectorProps {
  tenants: readonly Tenant[];
  isSystemAdmin?: boolean;
  onSelect: (tenant: { id: string; slug: string }) => Promise<void>;
}

const isSelectable = (tenant: Tenant) => !tenant.status || ["active", "enabled"].includes(tenant.status.toLowerCase());

/** Full-page workspace picker shown when no tenant is selected. */
export const TenantSelector = ({ tenants, isSystemAdmin, onSelect }: TenantSelectorProps) => {
  const { t } = useI18n("host");
  const [switchingId, setSwitchingId] = useState<string | null>(null);
  const handleSelect = async (tenant: Tenant) => {
    if (switchingId) return;
    setSwitchingId(tenant.id);
    try {
      await onSelect(tenant);
    } finally {
      setSwitchingId(null);
    }
  };
  return (
    <Center mih="100vh" p="xl">
      <Stack maw={480} w="100%">
        <Stack gap={4} ta="center">
          <Title order={2}>{t("tenantSelectorTitle")}</Title>
          <Text c="dimmed">{t("tenantSelectorBody")}</Text>
        </Stack>
        <Stack gap="sm">
          {tenants.map((tenant) => {
            const selectable = isSelectable(tenant);
            return (
              <UnstyledButton
                key={tenant.id}
                onClick={() => selectable && void handleSelect(tenant)}
                disabled={!selectable || switchingId !== null}
                aria-label={tenant.name}
              >
                <Card withBorder opacity={selectable ? 1 : 0.55}>
                  <Group justify="space-between" wrap="nowrap">
                    <div>
                      <Text fw={600}>{tenant.name}</Text>
                      <Text size="sm" c="dimmed" ff="monospace">
                        {tenant.slug}
                      </Text>
                    </div>
                    <Group gap="xs" wrap="nowrap">
                      {!selectable && <Badge color="yellow">{t("tenantSelectorUnavailable")}</Badge>}
                      {selectable && <IconChevronRight size={18} stroke={1.5} />}
                    </Group>
                  </Group>
                </Card>
              </UnstyledButton>
            );
          })}
        </Stack>
        {isSystemAdmin && (
          <Text ta="center">
            <Link to="/admin">
              <IconBuildingSkyscraper size={14} style={{ verticalAlign: "middle", marginRight: 4 }} />
              {t("tenantGoToSystemAdmin")}
            </Link>
          </Text>
        )}
      </Stack>
    </Center>
  );
};
