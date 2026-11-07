import { ActionIcon, Button, Group, Menu, Modal, Stack, Text, Textarea } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconBrandMessenger, IconCopy, IconLinkOff, IconSend } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import type { ContactChannel } from "../api/contacts";
import { createMessengerLink, unlinkMessengerParticipant, writeOnMessenger } from "../api/messenger";
import "../i18n";

/** The longest text one Messenger message holds. */
const MAX_TEXT = 2000;

/** Whether the person's reply window is open now, as Communications said when it closes. */
const windowOpen = (channel: ContactChannel): boolean =>
  Boolean(channel.linked && channel.participantId && channel.replyWindowClosesAt) &&
  Date.parse(channel.replyWindowClosesAt as string) >= Date.now();

/** The Messenger mark of a contact whose person is linked on at least one channel. */
export const MessengerMark = ({ channels }: { channels: ContactChannel[] | undefined }) => {
  const { t } = useI18n("customers");
  const linked = (channels ?? []).filter((c) => c.linked);
  if (linked.length === 0) return null;
  return (
    <IconBrandMessenger
      size={14}
      aria-label={t("messengerLinked", { channels: linked.map((c) => c.name).join(", ") })}
      role="img"
      style={{ verticalAlign: "text-bottom", marginLeft: 6 }}
    />
  );
};

/**
 * A contact's Messenger menu in the row of a customer's contacts (Messenger design N22): *Write on
 * Messenger*, enabled while the window of the linked person is open and otherwise saying who must write
 * first; *Copy Messenger link* for each channel, which is how a contact is linked in the first place;
 * and *Unlink*. A channel is shown with its name when there are several.
 */
export const ContactMessengerMenu = ({
  contactId,
  contactName,
  channels,
  customerId,
}: {
  contactId: number;
  contactName: string;
  channels: ContactChannel[];
  customerId: number;
}) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const [writing, setWriting] = useState<ContactChannel | null>(null);
  const [text, setText] = useState("");
  const several = channels.length > 1;

  const refresh = () => queryClient.invalidateQueries({ queryKey: ["customers", customerId, "contacts"] });
  const failed = (title: string) => (error: Error) =>
    notifications.show({ color: "red", title, message: error.message });

  const copy = useMutation({
    mutationFn: async (channel: ContactChannel) => {
      const link = await createMessengerLink(channel.channelId, contactId);
      await navigator.clipboard.writeText(link.url);
    },
    onSuccess: () =>
      notifications.show({
        color: "teal",
        title: t("messengerLinkCopied"),
        message: t("messengerLinkCopiedMessage", { name: contactName }),
      }),
    onError: failed(t("messengerLinkFailed")),
  });
  const unlink = useMutation({
    mutationFn: (channel: ContactChannel) => unlinkMessengerParticipant(channel.participantId as string),
    onSuccess: refresh,
    onError: failed(t("messengerUnlinkFailed")),
  });
  const write = useMutation({
    mutationFn: (channel: ContactChannel) =>
      writeOnMessenger(channel.participantId as string, text.trim(), crypto.randomUUID()),
    onSuccess: async (created) => {
      setWriting(null);
      setText("");
      await refresh();
      navigate({ href: `/communications/inbox?conversationId=${encodeURIComponent(created.conversationId)}` });
    },
    onError: (error) => {
      notifications.show({ color: "red", title: t("messengerWriteFailed"), message: error.message });
      void refresh();
    },
  });

  // Each item is offered to a caller who holds what Communications will ask of
  // them (Messenger design N22): writing needs conversations-reply, the link and
  // Unlink need conversations-manage. A channel with nothing to offer is left out,
  // and a menu with nothing at all is not shown.
  const offered = channels.map((channel) => {
    const person = channel.linked && Boolean(channel.participantId);
    return {
      channel,
      write: person && channel.canWrite === true,
      copy: channel.canManage === true,
      unlink: person && channel.canManage === true,
    };
  });
  const visible = offered.filter((o) => o.write || o.copy || o.unlink);
  if (visible.length === 0) return null;

  return (
    <>
      <Menu position="bottom-end" withinPortal>
        <Menu.Target>
          <ActionIcon variant="subtle" color="gray" aria-label={t("messengerFor", { name: contactName })}>
            <IconBrandMessenger size={16} />
          </ActionIcon>
        </Menu.Target>
        <Menu.Dropdown>
          {visible.map(({ channel, write: canWrite, copy: canCopy, unlink: canUnlink }) => (
            <div key={channel.channelId}>
              {several && <Menu.Label>{channel.name}</Menu.Label>}
              {canWrite &&
                (windowOpen(channel) ? (
                  <Menu.Item leftSection={<IconSend size={14} />} onClick={() => setWriting(channel)}>
                    {t("writeOnMessenger")}
                  </Menu.Item>
                ) : (
                  <Menu.Item disabled leftSection={<IconSend size={14} />}>
                    <Text size="sm" style={{ whiteSpace: "normal", maxWidth: 260 }}>
                      {t("messengerMustWriteFirst", { name: contactName, page: channel.name })}
                    </Text>
                  </Menu.Item>
                ))}
              {canCopy && (
                <Menu.Item leftSection={<IconCopy size={14} />} onClick={() => copy.mutate(channel)}>
                  {t("copyMessengerLink")}
                </Menu.Item>
              )}
              {canUnlink && (
                <Menu.Item color="red" leftSection={<IconLinkOff size={14} />} onClick={() => unlink.mutate(channel)}>
                  {t("unlinkMessenger")}
                </Menu.Item>
              )}
            </div>
          ))}
        </Menu.Dropdown>
      </Menu>

      <Modal
        opened={writing != null}
        onClose={() => setWriting(null)}
        title={t("writeOnMessengerTitle", { name: contactName, page: writing?.name ?? "" })}
      >
        <Stack gap="sm">
          <Textarea
            label={t("messengerMessage")}
            rows={4}
            maxLength={MAX_TEXT}
            value={text}
            onChange={(event) => setText(event.currentTarget.value)}
            data-autofocus
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setWriting(null)}>
              {t("cancel")}
            </Button>
            <Button
              loading={write.isPending}
              disabled={text.trim() === ""}
              onClick={() => writing && write.mutate(writing)}
            >
              {t("sendMessage")}
            </Button>
          </Group>
        </Stack>
      </Modal>
    </>
  );
};
