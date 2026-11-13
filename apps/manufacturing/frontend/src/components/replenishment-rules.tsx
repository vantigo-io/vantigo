import { Alert, Group, Stack, Switch, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { PageSection } from "@vantigo/frontend-shell";
import { putReplenishmentRule, replenishmentRulesQueryOptions } from "../api/replenishment";
import { manufacturingSitesQueryOptions } from "../api/sites";
import { refusalMessage, useInvalidateManufacturing, useManufacturingFormat } from "../lib/format";

/**
 * A BoM's variant's automatic replenishment per site (manufacturing phase 2
 * M111): Create automatically, and Confirm too, each off by default, with a
 * warning that the nightly run then creates orders nobody has looked at.
 * Written by a holder of `manufacturing:orders-manage`; read by anyone.
 */
export const ReplenishmentRules = ({ variantId, editable }: { variantId: number; editable: boolean }) => {
  const { t } = useManufacturingFormat();
  const invalidate = useInvalidateManufacturing();
  const rules = useQuery(replenishmentRulesQueryOptions(variantId));
  const sites = useQuery(manufacturingSitesQueryOptions());
  const saving = useMutation({
    mutationFn: (rule: { siteId: number; autoCreate: boolean; autoConfirm: boolean }) =>
      putReplenishmentRule(variantId, rule.siteId, rule.autoCreate, rule.autoConfirm),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("ruleSaved"), message: "" });
      await invalidate();
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusalMessage(t, error) }),
  });
  if (!rules.data || rules.data.items.length === 0) return null;
  const siteName = (id: number) => {
    const site = sites.data?.find((one) => one.id === id);
    return site ? `${site.code} · ${site.name}` : String(id);
  };
  return (
    <PageSection title={t("replenishmentRules")}>
      <Stack gap="sm" data-assist="manufacturing.bom.replenishment">
        <Text size="sm" c="dimmed">
          {t("replenishmentRulesHint")}
        </Text>
        {rules.data.items.some((rule) => rule.autoCreate) && (
          <Alert color="yellow" icon={<IconAlertTriangle size={16} />}>
            {t("replenishmentRulesWarning")}
          </Alert>
        )}
        <Table>
          <Table.Tbody>
            {rules.data.items.map((rule) => (
              <Table.Tr key={rule.siteId}>
                <Table.Td>{siteName(rule.siteId)}</Table.Td>
                <Table.Td>
                  <Group gap="lg">
                    <Switch
                      label={t("autoCreate")}
                      checked={rule.autoCreate}
                      disabled={!editable || saving.isPending}
                      onChange={(event) =>
                        saving.mutate({
                          siteId: rule.siteId,
                          autoCreate: event.currentTarget.checked,
                          autoConfirm: event.currentTarget.checked && rule.autoConfirm,
                        })
                      }
                    />
                    <Switch
                      label={t("autoConfirm")}
                      checked={rule.autoConfirm}
                      disabled={!editable || !rule.autoCreate || saving.isPending}
                      onChange={(event) =>
                        saving.mutate({
                          siteId: rule.siteId,
                          autoCreate: true,
                          autoConfirm: event.currentTarget.checked,
                        })
                      }
                    />
                  </Group>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Stack>
    </PageSection>
  );
};
