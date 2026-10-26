import { Text } from "@mantine/core";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { NotFoundError, PLANNING_QUERY_KEY } from "../api/request";
import { type PlanningWish, withdrawMyWish } from "../api/wishes";
import { NONE, usePlanningFormat } from "../lib/format";

/**
 * A wish in words: off which project from when, or something new from when.
 * An off wish whose project the server could not name reads *Project N*
 * (`projectName`).
 */
export const useWishText = () => {
  const { t, projectName, date } = usePlanningFormat();
  return (wish: PlanningWish) =>
    wish.kind === "off"
      ? t("wishOffFrom", {
          project: wish.project ? projectName(wish.project) : NONE,
          date: date(wish.fromDate),
        })
      : t("wishNewFrom", { date: date(wish.fromDate) });
};

/** A refused wish write's message: a wish gone meanwhile (404) in the catalog's words, else the server's. */
export const useWishRefusal = () => {
  const { t } = usePlanningFormat();
  return (error: Error) => (error instanceof NotFoundError ? t("wishGone") : error.message);
};

/**
 * Withdrawing a wish of one's own after asking (S21): it is deleted. A
 * refusal — withdrawn or resolved meanwhile — is said in a notification and
 * the page re-read.
 */
export const useWithdrawWish = () => {
  const { t } = usePlanningFormat();
  const queryClient = useQueryClient();
  const refusal = useWishRefusal();
  const withdraw = useMutation({
    mutationFn: (wish: PlanningWish) => withdrawMyWish(wish.id),
    onSuccess: async () => {
      notifications.show({ color: "teal", title: t("wishWithdrawn"), message: "" });
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
    },
    onError: async (error: Error) => {
      notifications.show({ color: "red", title: t("couldNotSave"), message: refusal(error) });
      await queryClient.invalidateQueries({ queryKey: [PLANNING_QUERY_KEY] });
    },
  });
  return (wish: PlanningWish) =>
    modals.openConfirmModal({
      title: t("withdrawWishTitle"),
      children: <Text size="sm">{t("withdrawWishExplained")}</Text>,
      labels: { confirm: t("withdraw"), cancel: t("cancel") },
      confirmProps: { color: "red" },
      onConfirm: () => withdraw.mutate(wish),
    });
};
