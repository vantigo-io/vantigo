-- +goose Up
-- Communications' schema, single-tenant and outbound-only: channels
-- (SMTP-only), participants, conversations and their messages, the
-- composer's staged attachments, delivery and its event log, tags, read
-- state, suppressions, composer idempotency, the durable object-cleanup
-- ledger, the outbox and the AI interaction audit trail. 19 tables — the
-- .NET module's 21 minus inbound_receipts and inbound_email_jobs, whose only
-- writers (the Mailgun inbound webhook and the inbound worker) are out of
-- scope for this port (communications inventory §9). Every tenant_id column,
-- key, index and RLS policy from the .NET EF configuration is dropped; no
-- primary key in this module ever contained tenant_id, so the drop touches
-- indexes and unique constraints only (inventory §7's structural-difference
-- note, §8). See docs/superpowers/specs/2026-09-13-communications-inventory.md
-- §7-§10, §19 and docs/superpowers/specs/2026-09-13-communications-design.md
-- §3, D2, D4, D5, D6.
CREATE SCHEMA communications;

-- Every entity's id is an application-generated uuid (no DB default), the
-- same convention identity's schema already uses: .NET's entities call
-- Guid.NewGuid() in their constructors, so the DDL default stays absent and
-- the Go port generates ids the same way (google/uuid.New()).

CREATE TABLE communications.channels (
    id           uuid         PRIMARY KEY,
    type         varchar(30)  NOT NULL,
    address      varchar(320) NOT NULL,
    display_name varchar(200),
    -- These three are the only real DB defaults in the entire .NET schema
    -- (inventory §10 item 10); every other default below is new in this
    -- port (D4). provider narrows to 'smtp' in practice post-port (only
    -- SMTP channels can ever be created, inventory §9 item 1) but the
    -- column keeps its full width and no CHECK, so a future non-SMTP
    -- provider is additive without a migration.
    provider     varchar(20)  NOT NULL DEFAULT 'smtp',
    is_default   boolean      NOT NULL DEFAULT false,
    is_active    boolean      NOT NULL DEFAULT true,
    created_at   timestamptz  NOT NULL
);
-- .NET's unique key was (tenant_id, Type, Address); with tenant_id gone,
-- (Type, Address) alone (inventory §8).
CREATE UNIQUE INDEX ux_channels_type_address ON communications.channels (type, address);
-- .NET's partial unique key was (tenant_id, Type, IsDefault) WHERE
-- IsDefault; with tenant_id gone this degenerates to "at most one default
-- channel per Type" (inventory §8 — the residual IsDefault column is
-- constant true inside the filter). The composer's channel fallback
-- (OrderByDescending(IsDefault).ThenBy(CreatedAt)) is only deterministic
-- because of this index (inventory §10 item 1).
CREATE UNIQUE INDEX ux_channels_type_is_default ON communications.channels (type, is_default) WHERE is_default;

CREATE TABLE communications.channel_credentials (
    id                 uuid PRIMARY KEY,
    channel_id         uuid NOT NULL REFERENCES communications.channels (id) ON DELETE CASCADE,
    settings_json      text NOT NULL,
    secret_ciphertext  text NOT NULL,
    created_at         timestamptz NOT NULL
);
-- .NET carried two uniques here — (tenant_id, ChannelId) and a second,
-- EF-1:1-convention unique on ChannelId alone. Dropping tenant_id collapses
-- them into an exact duplicate; emit one (inventory §8).
CREATE UNIQUE INDEX ux_channel_credentials_channel_id ON communications.channel_credentials (channel_id);

CREATE TABLE communications.participants (
    id           uuid PRIMARY KEY,
    -- channels with any history (a channel-scoped participant) cannot be
    -- deleted; channels are deactivated via is_active = false instead
    -- (inventory §10 item 9). One of the schema's three Restrict FKs
    -- (dispatch correction 2 — .NET's only *RESTRICT-to-a-child* FK is
    -- message_events.delivery_id below, but this and conversations.channel_id
    -- are Restrict too).
    channel_id   uuid NOT NULL REFERENCES communications.channels (id) ON DELETE RESTRICT,
    address      varchar(320) NOT NULL,
    display_name varchar(200),
    -- Cross-module reference to Customers, resolved through
    -- contracts.CustomerDirectory at runtime, never a DB FK (Global
    -- Constraints, "one schema per module"; inventory §7).
    contact_id   integer,
    created_at   timestamptz NOT NULL
);
-- .NET's unique key was (tenant_id, ChannelId, Address); reduces to
-- (ChannelId, Address) and also subsumes the EF convention index that would
-- otherwise sit on ChannelId alone (inventory §8, §10's convention-index
-- note).
CREATE UNIQUE INDEX ux_participants_channel_id_address ON communications.participants (channel_id, address);
-- .NET's index was (tenant_id, ContactId); reduces to (ContactId).
CREATE INDEX ix_participants_contact_id ON communications.participants (contact_id);

CREATE TABLE communications.conversations (
    id                              uuid PRIMARY KEY,
    -- Restrict, like participants.channel_id above (inventory §10 item 9).
    channel_id                      uuid NOT NULL REFERENCES communications.channels (id) ON DELETE RESTRICT,
    subject                         varchar(998),
    -- D4: Conversation.Status = "open" is a C#-only initialiser on a
    -- NOT NULL column with no DDL counterpart in .NET (inventory §10 item
    -- 10) — a raw Go INSERT that omits the column would otherwise hit a
    -- NOT NULL violation instead of the value every write path actually
    -- produces.
    status                          varchar(20)  NOT NULL DEFAULT 'open',
    -- Identity lives in another schema (Global Constraints); no DB FK.
    assigned_user_id                uuid,
    customer_id                     integer,
    customer_association_source     varchar(20),
    suggested_customer_id           integer,
    suggested_customer_confidence   double precision,
    suggested_customer_reasoning    text,
    last_activity_at                timestamptz  NOT NULL,
    preview_text                    varchar(500),
    created_at                      timestamptz  NOT NULL,
    CONSTRAINT ck_conversations_customer_association_source
        CHECK (customer_association_source IS NULL OR customer_association_source IN ('manual', 'automatic')),
    CONSTRAINT ck_conversations_suggested_customer_confidence
        CHECK (suggested_customer_confidence IS NULL OR (suggested_customer_confidence >= 0 AND suggested_customer_confidence <= 1))
);
-- The conversations feed index (spec D6; dispatch correction 1). .NET's
-- index is (tenant_id, Status, LastActivityAt) with a *positional*
-- descending array {false, true, true} — tenant_id ASC, Status DESC,
-- LastActivityAt DESC. Dropping the leading tenant_id column means the
-- remaining flags must shift left too, to {true, true}: the result is
-- BOTH columns descending, not "(status, last_activity_at DESC)" — the
-- natural-looking mistranslation the inventory warns is "plausible-looking
-- and wrong" (inventory §7, §8, §19.1 item 5). Pinned by
-- TestCommunicationsBaseline_ConversationsFeedIndexIsFullyDescending in
-- internal/db/schema_test.go, which reads the direction off pg_index
-- (indoption's DESC bit) rather than trusting this DDL text.
CREATE INDEX ix_conversations_status_last_activity_at ON communications.conversations (status DESC, last_activity_at DESC);
CREATE INDEX ix_conversations_customer_id ON communications.conversations (customer_id);
CREATE INDEX ix_conversations_suggested_customer_id ON communications.conversations (suggested_customer_id);
-- EF's convention index on the channel_id FK; nothing else covers it.
CREATE INDEX ix_conversations_channel_id ON communications.conversations (channel_id);

-- D5: kept — table and every read path survive even though its only writer
-- (the contact linker, reached only from the removed inbound processor) is
-- gone. Dropping it would change response shapes the frontend still reads
-- for no benefit, and it becomes live again the day an inbound provider
-- returns (inventory §9, §19.3).
CREATE TABLE communications.conversation_customer_candidates (
    conversation_id uuid NOT NULL REFERENCES communications.conversations (id) ON DELETE CASCADE,
    customer_id     integer NOT NULL,
    created_at      timestamptz NOT NULL,
    PRIMARY KEY (conversation_id, customer_id)
);
CREATE INDEX ix_conversation_customer_candidates_customer_id ON communications.conversation_customer_candidates (customer_id);

CREATE TABLE communications.conversation_messages (
    id                       uuid PRIMARY KEY,
    conversation_id          uuid NOT NULL REFERENCES communications.conversations (id) ON DELETE CASCADE,
    -- Dispatch correction 3: .NET's Direction domain is
    -- {inbound, outbound, internal_note}; this port's only message
    -- producers are the composer's reply and note paths, so "inbound" can
    -- never be written. The CHECK below is *new* — .NET has no CHECK here
    -- at all (inventory §10 item 7 names the two CHECKs on conversations as
    -- "the only CHECKs in the schema") — added deliberately to encode what
    -- this port can actually produce, and deliberately narrower than
    -- .NET's: it must never be loosened to admit 'inbound' again without an
    -- inbound producer to match it.
    direction                varchar(20) NOT NULL CHECK (direction IN ('outbound', 'internal_note')),
    participant_id           uuid REFERENCES communications.participants (id) ON DELETE SET NULL,
    -- No FK: identity lives in another schema.
    author_user_id           uuid,
    subject                  varchar(998),
    text_body                text,
    html_body                text,
    channel_metadata_json    jsonb,
    -- Dispatch correction 3: this column's only .NET writer is the inbound
    -- processor (SV/InboundEmailJobProcessor.cs), which is out of scope, so
    -- every row this port ever writes leaves it NULL. Kept anyway: it is
    -- contract-visible (inventory §9 item 6) and the column costs nothing
    -- sitting unwritten. No CHECK forces it NULL — that would be an
    -- invariant .NET never had either, and a future inbound provider should
    -- not need a migration to start writing it again.
    raw_payload_storage_key  varchar(1000),
    occurred_at              timestamptz NOT NULL,
    created_at               timestamptz NOT NULL,
    rfc_message_id           varchar(998)
);
CREATE INDEX ix_conversation_messages_conversation_id_occurred_at ON communications.conversation_messages (conversation_id, occurred_at);
-- Deliberately NOT unique (inventory §8): more than one message may share
-- an RfcMessageId in .NET today, and a unique index here would reject valid
-- rows.
CREATE INDEX ix_conversation_messages_rfc_message_id ON communications.conversation_messages (rfc_message_id);
-- EF's convention index on the participant_id FK; not subsumed by the
-- composite index above.
CREATE INDEX ix_conversation_messages_participant_id ON communications.conversation_messages (participant_id);

CREATE TABLE communications.conversation_participants (
    conversation_id uuid NOT NULL REFERENCES communications.conversations (id) ON DELETE CASCADE,
    participant_id  uuid NOT NULL REFERENCES communications.participants (id) ON DELETE CASCADE,
    -- D4: ConversationParticipant.Role = "participant" is a C#-only
    -- initialiser (inventory §10 item 10).
    role            varchar(30) NOT NULL DEFAULT 'participant',
    PRIMARY KEY (conversation_id, participant_id)
);
-- .NET's index was (tenant_id, ParticipantId) plus a duplicate EF
-- convention index on ParticipantId; both collapse to one (inventory §8).
CREATE INDEX ix_conversation_participants_participant_id ON communications.conversation_participants (participant_id);

-- message_attachments and attachment_uploads each carried six
-- scanner-related columns in .NET (ScanStatus, ScanAttempts, NextScanAt,
-- ScanLeaseId, ScanLeaseUntil, ScanError — inventory §5.7). This port drops
-- the ClamAV client and the scanner worker entirely (design doc §1), and
-- with them go the four columns only the scanner ever touched —
-- scan_attempts, scan_lease_id, scan_lease_until, scan_error: "no
-- non-scanner writer exists on either table. Fully dead." (inventory
-- §5.7). scan_status and next_scan_at are kept on both tables, against the
-- design doc's own summary line ("losing their six scan columns each",
-- design §3): D2 requires scan_status to exist and be readable (Task 6's
-- ready = scan_status == "clean" derivation, and the download/reply/outbox
-- gates in §5.5), and D4 explicitly calls out next_scan_at as a NOT NULL
-- column needing a DDL default. Keeping these two and dropping the other
-- four is the reading this port commits to; see the task report for the
-- full reasoning.
CREATE TABLE communications.message_attachments (
    id            uuid PRIMARY KEY,
    message_id    uuid NOT NULL REFERENCES communications.conversation_messages (id) ON DELETE CASCADE,
    file_name     varchar(500) NOT NULL,
    content_type  varchar(200) NOT NULL,
    size_bytes    bigint NOT NULL,
    content_hash  varchar(128) NOT NULL,
    content_id    varchar(500),
    storage_key   varchar(1000) NOT NULL,
    -- D2 (pre-flight ruling): .NET's initialiser is "pending", advanced to
    -- "clean" only by the scanner this port removes. With no scanner, the
    -- only surviving writer is the composer's reply promotion, which always
    -- writes "clean" (inventory §5.7) — so "clean" is the effective default
    -- here too, not .NET's "pending". A raw insert that forgot the column
    -- gets the value every real write path produces.
    scan_status   varchar(20) NOT NULL DEFAULT 'clean',
    -- D4: NextScanAt = DateTimeOffset.UtcNow is a C#-only initialiser
    -- (inventory §10 item 10, §5.7). Its only reader was the scanner's claim
    -- query, which is gone, so this column is now write-only — kept because
    -- D4 names it explicitly and dropping a column silently is a bigger
    -- surprise than carrying one nobody reads.
    next_scan_at  timestamptz NOT NULL DEFAULT now(),
    is_inline     boolean NOT NULL,
    created_at    timestamptz NOT NULL
);
-- .NET's index was (tenant_id, MessageId); reduces to (MessageId) and
-- duplicates the EF convention index on the same column (inventory §8).
CREATE INDEX ix_message_attachments_message_id ON communications.message_attachments (message_id);
-- No (scan_status, next_scan_at) index: .NET's was the scanner's claim
-- query index (inventory §8), and nothing queries this predicate once the
-- scanner worker is gone.

CREATE TABLE communications.attachment_uploads (
    id                   uuid PRIMARY KEY,
    conversation_id      uuid NOT NULL REFERENCES communications.conversations (id) ON DELETE CASCADE,
    -- No FK: identity lives in another schema.
    uploaded_by_user_id  uuid NOT NULL,
    file_name            varchar(500) NOT NULL,
    content_type         varchar(200) NOT NULL,
    size_bytes           bigint NOT NULL,
    content_hash         varchar(128) NOT NULL,
    content_id           varchar(500),
    storage_key          varchar(1000) NOT NULL,
    -- D2: new uploads are staged "clean" because there is nothing left to
    -- scan them (design doc D2) — not .NET's "pending". This is the literal
    -- ruling the dispatch names: "attachment_uploads.scan_status defaults
    -- to 'clean', not .NET's 'pending'." Consequence Task 6 relies on:
    -- ready in the contract is a derived duplicate (ready == scan_status ==
    -- "clean"), so every staged upload is permanently ready and the
    -- attachments_not_ready 409 gate at reply time never actually fires
    -- post-port.
    scan_status          varchar(20) NOT NULL DEFAULT 'clean',
    -- D4, same reasoning as message_attachments.next_scan_at above.
    next_scan_at         timestamptz NOT NULL DEFAULT now(),
    is_inline            boolean NOT NULL,
    -- Unbounded in .NET (no HasMaxLength anywhere; inventory §10 item 12) —
    -- the 200-character cap is endpoint-side validation only, so the
    -- column stays text, not varchar(200).
    idempotency_key      text NOT NULL,
    expires_at           timestamptz NOT NULL,
    created_at           timestamptz NOT NULL
);
-- .NET's unique key was (tenant_id, UploadedByUserId, IdempotencyKey);
-- reduces to (UploadedByUserId, IdempotencyKey) — still per-user scoped,
-- and deliberately NOT the same shape as idempotency_records' global
-- unique on Key alone (inventory §8's closing asymmetry note, §19.3).
CREATE UNIQUE INDEX ux_attachment_uploads_uploaded_by_user_id_idempotency_key ON communications.attachment_uploads (uploaded_by_user_id, idempotency_key);
-- .NET's index was (tenant_id, ConversationId, UploadedByUserId,
-- ExpiresAt); reduces to the same tuple without tenant_id, and subsumes
-- the EF convention index that would otherwise sit on ConversationId alone.
CREATE INDEX ix_attachment_uploads_conversation_id_uploaded_by_user_id_expires_at ON communications.attachment_uploads (conversation_id, uploaded_by_user_id, expires_at);
-- No (scan_status, next_scan_at) index, same reasoning as message_attachments.

CREATE TABLE communications.message_deliveries (
    id                        uuid PRIMARY KEY,
    message_id                uuid NOT NULL REFERENCES communications.conversation_messages (id) ON DELETE CASCADE,
    recipient_address         varchar(320) NOT NULL,
    recipient_type            varchar(10) NOT NULL,
    recipient_participant_id  uuid REFERENCES communications.participants (id) ON DELETE SET NULL,
    -- D4: MessageDelivery.Status = "queued" is a C#-only initialiser
    -- (inventory §10 item 10).
    status                    varchar(40) NOT NULL DEFAULT 'queued',
    attempts                  integer NOT NULL,
    last_error                text,
    accepted_at               timestamptz,
    created_at                timestamptz NOT NULL
);
-- Both reduce from a tenant-qualified index to the bare FK column and
-- duplicate their EF convention indexes (inventory §8).
CREATE INDEX ix_message_deliveries_message_id ON communications.message_deliveries (message_id);
CREATE INDEX ix_message_deliveries_recipient_participant_id ON communications.message_deliveries (recipient_participant_id);

CREATE TABLE communications.message_events (
    id           uuid PRIMARY KEY,
    message_id   uuid NOT NULL REFERENCES communications.conversation_messages (id) ON DELETE CASCADE,
    -- Dispatch correction 2: message_events.delivery_id is the schema's
    -- only RESTRICT FK *to a child of the two tables retention batch-deletes
    -- together* (message_events and message_deliveries) — the other two
    -- Restrict FKs in this schema (conversations.channel_id and
    -- participants.channel_id, above) sit outside that pair and do not
    -- constrain delete order the way this one does. Every other FK in the
    -- schema is Cascade or SetNull (inventory §10 item 8). This is why
    -- retention must delete message_events before message_deliveries
    -- (inventory §12.1, §19.1 item 6) — reversing the order turns retention
    -- into a permanent FK violation. Pinned by
    -- TestCommunicationsBaseline_MessageEventsDeliveryIdIsRestrict in
    -- internal/db/schema_test.go.
    delivery_id  uuid REFERENCES communications.message_deliveries (id) ON DELETE RESTRICT,
    event_type   varchar(60) NOT NULL,
    occurred_at  timestamptz NOT NULL,
    -- Deliberately text, not jsonb, unlike conversation_messages'
    -- channel_metadata_json: jsonb rejects malformed JSON at write time and
    -- text does not, so unifying the two would change write-time validation
    -- (inventory §10, "type notes a port should not normalise away").
    data_json    text
);
CREATE INDEX ix_message_events_message_id_occurred_at ON communications.message_events (message_id, occurred_at);
-- EF's convention index on the delivery_id FK; not subsumed above.
CREATE INDEX ix_message_events_delivery_id ON communications.message_events (delivery_id);

-- The only entity with no created_at (inventory §7).
CREATE TABLE communications.tags (
    id    uuid PRIMARY KEY,
    name  varchar(100) NOT NULL,
    color varchar(20)
);
-- .NET's unique key was (tenant_id, Name); reduces to (Name) — tag names
-- become installation-unique (inventory §8).
CREATE UNIQUE INDEX ux_tags_name ON communications.tags (name);

CREATE TABLE communications.conversation_tags (
    conversation_id uuid NOT NULL REFERENCES communications.conversations (id) ON DELETE CASCADE,
    tag_id          uuid NOT NULL REFERENCES communications.tags (id) ON DELETE CASCADE,
    PRIMARY KEY (conversation_id, tag_id)
);
-- No declared index in .NET; only the EF convention index on tag_id.
CREATE INDEX ix_conversation_tags_tag_id ON communications.conversation_tags (tag_id);

-- No FK on user_id (identity lives in another schema) and no secondary
-- index in .NET (inventory §7).
CREATE TABLE communications.conversation_read_states (
    conversation_id uuid NOT NULL REFERENCES communications.conversations (id) ON DELETE CASCADE,
    user_id         uuid NOT NULL,
    last_read_at    timestamptz NOT NULL,
    PRIMARY KEY (conversation_id, user_id)
);

CREATE TABLE communications.suppressions (
    id                        uuid PRIMARY KEY,
    normalized_email_address  varchar(320) NOT NULL,
    reason                    varchar(500),
    created_at                timestamptz NOT NULL
);
-- .NET's unique key was (tenant_id, NormalizedEmailAddress); reduces to
-- (NormalizedEmailAddress) (inventory §8). The stored value is uppercased
-- (Trim().ToUpperInvariant(), spec D7) — a different normalisation than the
-- contact linker's lowercase form used elsewhere in the module; both are
-- ported exactly as they are (inventory §10 item 4, §19.1 item 7).
CREATE UNIQUE INDEX ux_suppressions_normalized_email_address ON communications.suppressions (normalized_email_address);

CREATE TABLE communications.idempotency_records (
    id                   uuid PRIMARY KEY,
    key                  varchar(200) NOT NULL,
    payload_fingerprint  varchar(64) NOT NULL,
    conversation_id      uuid NOT NULL REFERENCES communications.conversations (id) ON DELETE CASCADE,
    -- No FK: message_id has no FK in .NET either (only an index) — this is
    -- exactly why retention must delete idempotency_records explicitly by
    -- message_id, since nothing cascades it (inventory §7, §12.1's "step 6
    -- is likewise mandatory" note).
    message_id           uuid NOT NULL,
    created_at           timestamptz NOT NULL
);
-- .NET's unique key was (tenant_id, Key); reduces to (Key) alone — a
-- global, not per-user, idempotency key. Deliberately asymmetric with
-- attachment_uploads' per-user unique above (inventory §8's closing note).
CREATE UNIQUE INDEX ux_idempotency_records_key ON communications.idempotency_records (key);
CREATE INDEX ix_idempotency_records_conversation_id ON communications.idempotency_records (conversation_id);
CREATE INDEX ix_idempotency_records_message_id ON communications.idempotency_records (message_id);

-- No FKs at all, deliberately: the row must outlive the message it
-- describes (inventory §7). No unique index on storage_key either — a
-- completed historical row is allowed to coexist with a live reservation
-- for a reused key; adding the "obvious" unique index would break that
-- legitimate case (inventory §10 item 11).
CREATE TABLE communications.attachment_cleanup_records (
    id                       uuid PRIMARY KEY,
    message_id               uuid,
    storage_key              varchar(1000) NOT NULL,
    -- D4: AttachmentCleanupRecord.Status = "pending" is a C#-only
    -- initialiser (inventory §10 item 10).
    status                   varchar(30) NOT NULL DEFAULT 'pending',
    attempts                 integer NOT NULL,
    next_attempt_at          timestamptz NOT NULL,
    lease_id                 varchar(100),
    lease_until              timestamptz,
    reservation_expires_at   timestamptz,
    last_error               text,
    created_at               timestamptz NOT NULL
);
-- .NET's index was (tenant_id, Status, CreatedAt); reduces to
-- (Status, CreatedAt), matching the cleanup worker's claim query ordering
-- (inventory §8).
CREATE INDEX ix_attachment_cleanup_records_status_created_at ON communications.attachment_cleanup_records (status, created_at);

CREATE TABLE communications.outbox_jobs (
    id                      uuid PRIMARY KEY,
    message_id              uuid NOT NULL REFERENCES communications.conversation_messages (id) ON DELETE CASCADE,
    -- D4: OutboxJob.Status = "pending" is a C#-only initialiser (inventory
    -- §10 item 10).
    status                  varchar(30) NOT NULL DEFAULT 'pending',
    attempts                integer NOT NULL,
    next_attempt_at         timestamptz NOT NULL,
    lease_id                varchar(100),
    lease_until             timestamptz,
    completed_at            timestamptz,
    last_error              text,
    created_at              timestamptz NOT NULL,
    -- Added by .NET's third migration (MIG3); cleared on claim and
    -- committed in its own write immediately before the external send, so a
    -- job re-claimed with it already set is a possible-duplicate send
    -- (inventory §7, design doc §4).
    delivery_attempted_at   timestamptz
);
-- .NET's index was (tenant_id, Status, NextAttemptAt); reduces to
-- (Status, NextAttemptAt) (inventory §8).
CREATE INDEX ix_outbox_jobs_status_next_attempt_at ON communications.outbox_jobs (status, next_attempt_at);
-- EF's convention index on the message_id FK; not subsumed by the
-- (status, next_attempt_at) index above.
CREATE INDEX ix_outbox_jobs_message_id ON communications.outbox_jobs (message_id);

CREATE TABLE communications.ai_interactions (
    id                    uuid PRIMARY KEY,
    conversation_id       uuid NOT NULL REFERENCES communications.conversations (id) ON DELETE CASCADE,
    -- SetNull, not Cascade: deleting a message during retention nulls this
    -- reference and keeps the AI audit row (inventory §7).
    message_id            uuid REFERENCES communications.conversation_messages (id) ON DELETE SET NULL,
    operation             varchar(50) NOT NULL,
    -- No FK: identity lives in another schema.
    requester_user_id     uuid,
    provider              varchar(50) NOT NULL,
    model                 varchar(150) NOT NULL,
    context_digest        varchar(64) NOT NULL,
    context_version       varchar(30) NOT NULL,
    result_summary        varchar(200),
    validation_summary    varchar(500),
    error_summary         varchar(200),
    duration_ms           bigint,
    input_token_count     integer,
    output_token_count    integer,
    created_at            timestamptz NOT NULL
);
-- .NET's indexes were (tenant_id, ConversationId, CreatedAt) and
-- (tenant_id, Operation); both reduce without tenant_id (inventory §8).
CREATE INDEX ix_ai_interactions_conversation_id_created_at ON communications.ai_interactions (conversation_id, created_at);
CREATE INDEX ix_ai_interactions_operation ON communications.ai_interactions (operation);
-- EF's convention index on the message_id FK; not subsumed by the
-- composite above (which leads with conversation_id, not message_id).
CREATE INDEX ix_ai_interactions_message_id ON communications.ai_interactions (message_id);

-- +goose Down
DROP SCHEMA communications CASCADE;
