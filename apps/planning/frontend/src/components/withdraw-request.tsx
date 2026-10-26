import { Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { PLANNING_QUERY_KEY, refusalCode, ruleRefusals } from "../api/request";
import { type PlanningRequest, withdrawRequest } from "../api/requests";
import { usePlanningFormat } from "../lib/format";

/**
 * Withdrawing a request after asking (S16): it stops being demand for good. A
 * refusal — the request was filled or withdrawn meanwhile — is said in a
 * notification and the lists re-read.
 */
export const useWithdrawRequest = () => {
  const { t } = usePlanningFormat();
  const queryClient = useQueryClient();
  const withdraw = useMutation({
    mutationFn: (request: PlanningRequest) => withdrawRequest(request.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("requestWithdrawn"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
    },
    onError: async (error: Error) => {
      const code = refusalCode(error);
      notifications.show({
        color: "red",
        title: t("couldNotSave"),
        message: code && ruleRefusals.includes(code) ? t(`refusal.${code}`) : error.message,
      });
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
    },
  });
  return (request: PlanningRequest) =>
    modals.openConfirmModal({
      title: t("withdrawRequestTitle"),
      children: <Text size="sm">{t("withdrawRequestExplained")}</Text>,
      labels: { confirm: t("withdraw"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => withdraw.mutate(request),
    });
};
