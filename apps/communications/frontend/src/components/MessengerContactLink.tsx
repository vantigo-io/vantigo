import { Alert, Button, Group, Select, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconLink, IconLinkOff } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  contactNameQueryOptions,
  customerContactOptionsQueryOptions,
  linkParticipantContact,
  type MessengerLink,
  unlinkParticipantContact,
} from "../api/contact-links";
import "../i18n";

/**
 * A Messenger conversation's participant and the contact it is linked to (Messenger design N20, N21),
 * in the thread's header: the contact's name with Unlink once linked; Link to contact — a picker of the
 * conversation's customer's contacts — while it is not; the contact a shared e-mail address suggests,
 * offered and never linked on its own; and the notice that the person used another contact's Messenger
 * link than the one they are linked to. The server checks everything again: this only offers.
 */
export function MessengerContactLink({
  conversationId,
  customerId,
  link,
}: {
  conversationId: string;
  customerId: number | null;
  link: MessengerLink;
}) {
  const { t } = useI18n("communications");
  const client = useQueryClient();
  const [picked, setPicked] = useState<string | null>(null);
  const linkedName = useQuery(contactNameQueryOptions(link.contactId));
  const suggestedName = useQuery(contactNameQueryOptions(link.contactId == null ? link.suggestedContactId : null));
  const referredName = useQuery(contactNameQueryOptions(link.referralContactId));
  const options = useQuery(customerContactOptionsQueryOptions(link.contactId == null ? customerId : null));

  const label = (id: number, name: string | null | undefined) => name ?? t("contactNumber", { id });
  const refresh = () => client.invalidateQueries({ queryKey: ["conversation", conversationId] });
  const onError = (error: Error) =>
    notifications.show({ color: "red", title: t("contactLinkFailed"), message: error.message });

  const attach = useMutation({
    mutationFn: (contactId: number) => linkParticipantContact(link.participantId, contactId, conversationId),
    onSuccess: async () => {
      setPicked(null);
      await refresh();
    },
    onError,
  });
  const detach = useMutation({
    mutationFn: () => unlinkParticipantContact(link.participantId),
    onSuccess: refresh,
    onError,
  });

  return (
    <div className="messenger-contact-link" data-testid="messenger-contact-link">
      {link.contactId != null ? (
        <Group gap="xs" mt="xs">
          <IconLink size={14} />
          <Text size="xs">{t("linkedToContact", { name: label(link.contactId, linkedName.data) })}</Text>
          {link.canManage && (
            <Button
              size="compact-xs"
              variant="subtle"
              color="gray"
              leftSection={<IconLinkOff size={13} />}
              loading={detach.isPending}
              onClick={() => detach.mutate()}
            >
              {t("unlinkContact")}
            </Button>
          )}
        </Group>
      ) : (
        <>
          {link.canManage && link.suggestedContactId != null && (
            <Group gap="xs" mt="xs">
              <Text size="xs" c="dimmed">
                {t("suggestedContactHint")}
              </Text>
              <Button
                size="compact-xs"
                variant="light"
                loading={attach.isPending}
                leftSection={<IconLink size={13} />}
                onClick={() => attach.mutate(link.suggestedContactId as number)}
              >
                {t("linkToNamedContact", { name: label(link.suggestedContactId, suggestedName.data) })}
              </Button>
            </Group>
          )}
          {link.canManage && customerId != null && (options.data?.length ?? 0) > 0 && (
            <Group gap="xs" mt="xs" align="end">
              <Select
                size="xs"
                aria-label={t("pickContact")}
                placeholder={t("pickContact")}
                data={options.data?.map((c) => ({ value: String(c.id), label: c.name })) ?? []}
                value={picked}
                onChange={setPicked}
                clearable
              />
              <Button
                size="xs"
                variant="light"
                disabled={picked == null}
                loading={attach.isPending}
                onClick={() => picked != null && attach.mutate(Number(picked))}
              >
                {t("linkToContact")}
              </Button>
            </Group>
          )}
        </>
      )}
      {link.referralContactId != null && link.contactId !== link.referralContactId && (
        <Alert color="yellow" mt="xs" p="xs" data-testid="referral-notice">
          {t("referralNotice", { name: label(link.referralContactId, referredName.data) })}
        </Alert>
      )}
    </div>
  );
}
