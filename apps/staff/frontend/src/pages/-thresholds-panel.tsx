import { Button, Table, Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { STAFF_QUERY_KEY } from "../api/request";
import {
  acknowledgeThreshold,
  type StaffThreshold,
  type StaffThresholdsAcknowledged,
  staffThresholds,
} from "../api/settings";
import { FieldGroup } from "../components/field-group";
import "../i18n";
import { refusalMessage } from "../lib/errors";

/**
 * The thresholds (design S20), `staff:manage`'s: each with the duty it
 * brings, and when it was acknowledged or **Acknowledge** — after a confirm,
 * since an acknowledgement is once and for good. The headcount is the
 * dashboard's, not shown here; a threshold need not be crossed to be
 * acknowledged.
 */
export const ThresholdsPanel = ({ acknowledged }: { acknowledged: StaffThresholdsAcknowledged }) => {
  const { t, formatters } = useI18n("staff");
  const queryClient = useQueryClient();
  const nameOf = (key: StaffThreshold) => t(`threshold_${key}`);

  const acknowledge = useMutation({
    mutationFn: acknowledgeThreshold,
    onSuccess: async (_, key) => {
      await queryClient.invalidateQueries({ queryKey: [STAFF_QUERY_KEY] });
      notifications.show({ color: "teal", title: t("thresholdAcknowledged", { name: nameOf(key) }), message: "" });
    },
    onError: (error, key) =>
      notifications.show({
        color: "red",
        title: t("couldNotAcknowledgeThreshold", { name: nameOf(key) }),
        message: refusalMessage(error),
      }),
  });

  const confirm = (key: StaffThreshold) =>
    modals.openConfirmModal({
      title: t("acknowledgeThresholdTitle", { name: nameOf(key) }),
      children: <Text size="sm">{t("acknowledgeThresholdExplained")}</Text>,
      labels: { confirm: t("acknowledge"), cancel: t("cancel") },
      onConfirm: () => acknowledge.mutate(key),
    });

  return (
    <FieldGroup title={t("thresholdsTitle")} description={t("thresholdsDescription")}>
      <Table.ScrollContainer minWidth={640}>
        <Table aria-label={t("thresholdsTitle")}>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t("threshold")}</Table.Th>
              <Table.Th>{t("thresholdDuty")}</Table.Th>
              <Table.Th>{t("status")}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {staffThresholds.map((key) => {
              const at = (acknowledged as Record<StaffThreshold, string | undefined>)[key];
              return (
                <Table.Tr key={key}>
                  <Table.Td>{nameOf(key)}</Table.Td>
                  <Table.Td>
                    <Text size="sm">{t(`thresholdDuty_${key}`)}</Text>
                  </Table.Td>
                  <Table.Td>
                    {at ? (
                      <Text size="sm" c="teal">
                        {t("thresholdAcknowledgedOn", {
                          date: formatters.formatDate(new Date(at), { dateStyle: "medium" }),
                        })}
                      </Text>
                    ) : (
                      <Button
                        size="compact-sm"
                        variant="light"
                        aria-label={t("acknowledgeThreshold", { name: nameOf(key) })}
                        loading={acknowledge.isPending && acknowledge.variables === key}
                        onClick={() => confirm(key)}
                      >
                        {t("acknowledge")}
                      </Button>
                    )}
                  </Table.Td>
                </Table.Tr>
              );
            })}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
    </FieldGroup>
  );
};
