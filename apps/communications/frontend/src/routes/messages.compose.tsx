import { Alert, Button, Card, Group, Stack, TagsInput, Text, TextInput, Title } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { RichTextEditor } from "@mantine/tiptap";
import { useMutation } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { useState } from "react";
import { type CreateMessageRequest, createMessage } from "../api/messages";
import type { ApiError } from "../api/request";

const emailPattern = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
type ComposeValues = { subject: string; to: string[]; cc: string[]; bcc: string[] };

const allRecipients = (values: ComposeValues) => [...values.to, ...values.cc, ...values.bcc];
const duplicateEmails = (values: ComposeValues) => {
  const counts = new Map<string, number>();
  for (const email of allRecipients(values)) {
    const normalized = email.trim().toLowerCase();
    counts.set(normalized, (counts.get(normalized) || 0) + 1);
  }
  return new Set([...counts.entries()].filter(([, count]) => count > 1).map(([email]) => email));
};

export function ComposePage() {
  const navigate = useNavigate();
  const [showCcBcc, setShowCcBcc] = useState(false);
  const [bodyError, setBodyError] = useState<string | null>(null);
  const [idempotencyKey, setIdempotencyKey] = useState(() => crypto.randomUUID());
  const editor = useEditor({
    extensions: [StarterKit.configure({ link: { openOnClick: false } })],
    content: "",
  });
  const form = useForm<ComposeValues>({
    initialValues: { subject: "", to: [], cc: [], bcc: [] },
    validate: {
      subject: (value) => (value.trim() ? null : "Subject is required"),
      to: (value, values) => {
        if (!value.length) return "At least one recipient is required";
        if (value.some((email) => !emailPattern.test(email.trim()))) return "Enter valid email addresses";
        const duplicates = duplicateEmails(values);
        return duplicates.size ? `Duplicate recipient: ${[...duplicates].join(", ")}` : null;
      },
      cc: (value, values) => {
        if (value.some((email) => !emailPattern.test(email.trim()))) return "Enter valid email addresses";
        const duplicates = duplicateEmails(values);
        return duplicates.size ? `Duplicate recipient: ${[...duplicates].join(", ")}` : null;
      },
      bcc: (value, values) => {
        if (value.some((email) => !emailPattern.test(email.trim()))) return "Enter valid email addresses";
        const duplicates = duplicateEmails(values);
        return duplicates.size ? `Duplicate recipient: ${[...duplicates].join(", ")}` : null;
      },
    },
  });
  const mutation = useMutation({
    mutationFn: (values: ComposeValues) => {
      setBodyError(null);
      const body: CreateMessageRequest = {
        subject: values.subject.trim(),
        textBody: editor?.getText() || "",
        htmlBody: editor?.getHTML() || "",
        to: values.to.map((email) => ({ email: email.trim() })),
        cc: values.cc.map((email) => ({ email: email.trim() })),
        bcc: values.bcc.map((email) => ({ email: email.trim() })),
      };
      return createMessage(body, idempotencyKey);
    },
    onSuccess: (result) => {
      notifications.show({ title: "Message queued", message: "Your message has been submitted." });
      setIdempotencyKey(crypto.randomUUID());
      void navigate({ to: "/messages/$messageId", params: { messageId: result.messageId } });
    },
    onError: (error: ApiError) => {
      if (error.status === 422 && error.code === "recipient_suppressed") {
        const suppressed = Array.isArray(error.fields?.recipients) ? error.fields.recipients : [];
        const message = suppressed.length ? `Suppressed recipients: ${suppressed.join(", ")}` : error.message;
        for (const field of ["to", "cc", "bcc"] as const) {
          const recipients = form.values[field].map((email) => email.toLowerCase());
          const matches = suppressed.filter((email) => recipients.includes(email.toLowerCase()));
          if (matches.length) form.setFieldError(field, `Suppressed recipients: ${matches.join(", ")}`);
        }
        if (!suppressed.length) notifications.show({ color: "red", title: "Message not sent", message });
        return;
      }
      if (error.status === 400 && error.fields) {
        for (const [field, value] of Object.entries(error.fields)) {
          const message = Array.isArray(value) ? value.join(", ") : value;
          if (field === "body") {
            setBodyError(message);
          } else if (field === "recipients" || /^to\[\d+\]$/.test(field)) {
            for (const recipientField of ["to", "cc", "bcc"] as const) form.setFieldError(recipientField, message);
          } else if (field === "to" || field === "cc" || field === "bcc" || field === "subject") {
            form.setFieldError(field, message);
          }
        }
        return;
      }
      if (error.status === 503) {
        notifications.show({
          color: "red",
          title: "No active mailbox",
          message: "Configure an active mailbox before sending.",
        });
        return;
      }
      notifications.show({ color: "red", title: "Message not sent", message: error.message });
    },
  });

  return (
    <Stack maw={900} mx="auto" gap="xl">
      <div>
        <Text className="eyebrow">New message</Text>
        <Title order={2}>Compose</Title>
        <Text c="dimmed">Send a message through the configured workspace mailbox.</Text>
      </div>
      {mutation.error?.status === 503 && (
        <Alert color="red" title="No active mailbox is configured">
          Ask an Owner to configure an active mailbox before sending messages.
        </Alert>
      )}
      <Card withBorder radius="lg" padding="lg">
        <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
          <Stack>
            <TextInput label="Subject" placeholder="Subject" required {...form.getInputProps("subject")} />
            <TagsInput
              label="To"
              placeholder="Add recipient"
              required
              maxTags={100}
              splitChars={[",", " "]}
              {...form.getInputProps("to")}
            />
            <Button type="button" variant="subtle" w="fit-content" onClick={() => setShowCcBcc((value) => !value)}>
              {showCcBcc ? "Hide Cc and Bcc" : "Add Cc or Bcc"}
            </Button>
            {showCcBcc && (
              <>
                <TagsInput
                  label="Cc"
                  placeholder="Add Cc recipient"
                  maxTags={100}
                  splitChars={[",", " "]}
                  {...form.getInputProps("cc")}
                />
                <TagsInput
                  label="Bcc"
                  placeholder="Add Bcc recipient"
                  maxTags={100}
                  splitChars={[",", " "]}
                  {...form.getInputProps("bcc")}
                />
              </>
            )}
            <div>
              <Text size="sm" fw={500} mb={5}>
                Body
              </Text>
              <RichTextEditor editor={editor} mih={240}>
                <RichTextEditor.Toolbar sticky stickyOffset={60}>
                  <RichTextEditor.ControlsGroup>
                    <RichTextEditor.Bold />
                    <RichTextEditor.Italic />
                    <RichTextEditor.BulletList />
                    <RichTextEditor.OrderedList />
                    <RichTextEditor.Link />
                    <RichTextEditor.Unlink />
                  </RichTextEditor.ControlsGroup>
                </RichTextEditor.Toolbar>
                <RichTextEditor.Content />
              </RichTextEditor>
              {bodyError && (
                <Text c="red" size="sm" mt={5}>
                  {bodyError}
                </Text>
              )}
            </div>
            <Group justify="flex-end">
              <Button type="submit" loading={mutation.isPending}>
                Send message
              </Button>
            </Group>
          </Stack>
        </form>
      </Card>
    </Stack>
  );
}

export const Route = createFileRoute("/messages/compose")({ component: ComposePage });
