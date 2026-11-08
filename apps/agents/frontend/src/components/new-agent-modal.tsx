import { Button, Group, Modal, Stack, TextInput } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { agentsQueryKey, createAgent } from "../api/agents";
import { isCompanyNameRequired } from "../api/request";
import { NAME_MAX } from "../lib/modes";
import { agentLinkOptions } from "../lib/routes";
import { useErrorText } from "./use-error-text";

/**
 * Makes an agent from a name; the persona is finished on its own page. The
 * workspace's model, the customer's language and the assistant wording are the
 * starting choices.
 */
export const NewAgentModal = ({ opened, onClose }: { opened: boolean; onClose: () => void }) => {
  const { t } = useI18n("agents");
  const errorText = useErrorText();
  const queryClient = useQueryClient();
  const navigate = useNavigate() as (options: unknown) => void;
  const [name, setName] = useState("");
  const trimmed = name.trim();

  const create = useMutation({
    mutationFn: () =>
      createAgent({ name: trimmed, style: "", languages: "customer", disclosureWording: "assistant", model: "" }),
    onSuccess: (agent) => {
      void queryClient.invalidateQueries({ queryKey: agentsQueryKey });
      notifications.show({ color: "teal", title: t("created"), message: "" });
      setName("");
      onClose();
      navigate(agentLinkOptions(agent.id));
    },
    onError: (error) =>
      notifications.show({
        color: "red",
        title: t("createFailed"),
        message: isCompanyNameRequired(error) ? t("companyNameRequired") : errorText(error),
      }),
  });

  return (
    <Modal opened={opened} onClose={onClose} title={t("newAgentTitle")}>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (trimmed !== "" && trimmed.length <= NAME_MAX) create.mutate();
        }}
      >
        <Stack>
          <TextInput
            label={t("agentName")}
            description={t("agentNameHelp")}
            value={name}
            onChange={(event) => setName(event.currentTarget.value)}
            maxLength={NAME_MAX}
            data-autofocus
            required
          />
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" disabled={trimmed === ""} loading={create.isPending}>
              {t("create")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
