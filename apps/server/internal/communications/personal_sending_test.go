package communications_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/communications/gen"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func TestPersonalSender_GetEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("unavailable when no mailbox provider wired", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		client := h.SignIn(t, "communications:conversations-view")

		res := client.Do(http.MethodGet, "/api/v1/communications/personal-sender", nil)
		if res.Status != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", res.Status, res.Body)
		}
		var resp gen.PersonalSenderResponse
		res.JSON(&resp)
		if resp.Available {
			t.Errorf("available = true, want false")
		}
		if resp.Reason == nil || *resp.Reason != "unavailable" {
			t.Errorf("reason = %v, want unavailable", resp.Reason)
		}
	})

	t.Run("available when mailbox provider reports connected", func(t *testing.T) {
		t.Parallel()
		fake := modtest.NewFakePersonalMailbox()
		h := newHarness(t, modtest.WithPersonalMailbox(fake))
		client, userID := h.SignInUser(t, "communications:conversations-view")

		fake.SetUser(userID, contracts.PersonalMailStatus{
			Available: true,
			Address:   "kari@firma.no",
			Mailbox:   "kari-oid",
		})

		res := client.Do(http.MethodGet, "/api/v1/communications/personal-sender", nil)
		if res.Status != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", res.Status, res.Body)
		}
		var resp gen.PersonalSenderResponse
		res.JSON(&resp)
		if !resp.Available {
			t.Errorf("available = false, want true")
		}
		if resp.Address == nil || *resp.Address != "kari@firma.no" {
			t.Errorf("address = %v, want kari@firma.no", resp.Address)
		}
	})
}

func TestPersonalSending_CreateConversation(t *testing.T) {
	t.Parallel()

	t.Run("409 connection_needed when personal sender unavailable", func(t *testing.T) {
		t.Parallel()
		fake := modtest.NewFakePersonalMailbox()
		h := newHarness(t, modtest.WithPersonalMailbox(fake))
		channelID := setupChannel(t, h)
		client := h.SignIn(t, "communications:conversations-reply")

		body := map[string]any{
			"channelId":        channelID,
			"subject":          "Subject",
			"textBody":         "Hello",
			"sendFromPersonal": true,
			"to":               []map[string]any{{"email": "customer@example.test"}},
		}
		res := client.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", uuid.NewString()))
		if res.Status != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body: %s", res.Status, res.Body)
		}
		if res.Code() != "connection_needed" {
			t.Errorf("code = %q, want connection_needed", res.Code())
		}
	})

	t.Run("422 too_large when personal send exceeds size limit", func(t *testing.T) {
		t.Parallel()
		fake := modtest.NewFakePersonalMailbox()
		h := newHarness(t, modtest.WithPersonalMailbox(fake))
		channelID := setupChannel(t, h)
		client, userID := h.SignInUser(t, "communications:conversations-reply")

		fake.SetUser(userID, contracts.PersonalMailStatus{
			Available: true,
			Address:   "kari@firma.no",
			Mailbox:   "kari-oid",
		})

		largeText := strings.Repeat("A", 550_000)
		largeHTML := "<p>" + strings.Repeat("B", 460_000) + "</p>"
		body := map[string]any{
			"channelId":        channelID,
			"subject":          "Subject",
			"textBody":         largeText,
			"htmlBody":         largeHTML,
			"sendFromPersonal": true,
			"to":               []map[string]any{{"email": "customer@example.test"}},
		}
		res := client.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", uuid.NewString()))
		if res.Status != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body: %s", res.Status, res.Body)
		}
		if res.Code() != "too_large" {
			t.Errorf("code = %q, want too_large", res.Code())
		}
	})

	t.Run("success stores send_as personal, from_address and from_display_name", func(t *testing.T) {
		t.Parallel()
		fake := modtest.NewFakePersonalMailbox()
		h := newHarness(t, modtest.WithPersonalMailbox(fake))
		channelID := setupChannel(t, h)
		client, userID := h.SignInUser(t, "communications:conversations-reply", "communications:conversations-view")

		fake.SetUser(userID, contracts.PersonalMailStatus{
			Available: true,
			Address:   "kari@firma.no",
			Mailbox:   "kari-oid",
		})

		body := map[string]any{
			"channelId":        channelID,
			"subject":          "Subject",
			"textBody":         "Hello from personal address",
			"sendFromPersonal": true,
			"to":               []map[string]any{{"email": "customer@example.test"}},
		}
		res := client.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", uuid.NewString()))
		if res.Status != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body: %s", res.Status, res.Body)
		}
		var mut conversationMutationJSON
		res.JSON(&mut)
		convID := mut.ConversationId
		msgID := *mut.MessageId

		// Verify in GetConversation
		detailRes := client.Do(http.MethodGet, "/api/v1/communications/conversations/"+convID, nil)
		if detailRes.Status != http.StatusOK {
			t.Fatalf("get conversation status = %d, want 200; body: %s", detailRes.Status, detailRes.Body)
		}
		var detail map[string]any
		detailRes.JSON(&detail)
		ps, _ := detail["personalSender"].(map[string]any)
		if ps == nil || ps["available"] != true {
			t.Errorf("personalSender.available = false, want true: %+v", ps)
		}
		if ps["address"] != "kari@firma.no" {
			t.Errorf("personalSender.address = %v, want kari@firma.no", ps["address"])
		}

		// Verify message in database
		var sendAs string
		var fromAddress *string
		err := h.Pool().QueryRow(context.Background(),
			`SELECT send_as, from_address FROM communications.conversation_messages WHERE id = $1`, uuid.MustParse(msgID)).
			Scan(&sendAs, &fromAddress)
		if err != nil {
			t.Fatalf("query message: %v", err)
		}
		if sendAs != "personal" {
			t.Errorf("send_as = %q, want personal", sendAs)
		}
		if fromAddress == nil || *fromAddress != "kari@firma.no" {
			t.Errorf("from_address = %v, want kari@firma.no", fromAddress)
		}
	})
}

func TestPersonalSending_OutboxWorker_Success(t *testing.T) {
	t.Parallel()

	fake := modtest.NewFakePersonalMailbox()
	h := newHarness(t, modtest.WithPersonalMailbox(fake))
	channelID := setupChannel(t, h)
	client, userID := h.SignInUser(t, "communications:conversations-reply", "communications:conversations-view")

	fake.SetUser(userID, contracts.PersonalMailStatus{
		Available: true,
		Address:   "kari@firma.no",
		Mailbox:   "kari-oid",
	})

	body := map[string]any{
		"channelId":        channelID,
		"subject":          "Subject",
		"textBody":         "Hello from personal send",
		"sendFromPersonal": true,
		"to":               []map[string]any{{"email": "customer@example.test"}},
	}
	res := client.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", uuid.NewString()))
	if res.Status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", res.Status, res.Body)
	}
	var mut conversationMutationJSON
	res.JSON(&mut)
	msgID := uuid.MustParse(*mut.MessageId)

	w := communications.NewOutboxWorker(h.Deps())
	processed, err := w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if !processed {
		t.Fatal("ProcessOne = false, want true")
	}

	job := readOutboxJob(t, h, msgID)
	if job.status != "completed" {
		t.Errorf("job status = %q, want completed", job.status)
	}

	deliveries := readDeliveries(t, h, msgID)
	if len(deliveries) != 1 || deliveries[0].status != "relay_accepted" {
		t.Errorf("deliveries = %+v, want 1 relay_accepted", deliveries)
	}

	sends := fake.Sent()
	if len(sends) != 1 {
		t.Fatalf("sends count = %d, want 1", len(sends))
	}
	send := sends[0]
	if send.UserID != userID {
		t.Errorf("send.UserID = %s, want %s", send.UserID, userID)
	}
	mimeStr := string(send.MIME)
	if !strings.Contains(mimeStr, "kari@firma.no") {
		t.Errorf("MIME lacks From kari@firma.no:\n%s", mimeStr)
	}
	if !strings.Contains(mimeStr, "Reply-To:") {
		t.Errorf("MIME lacks Reply-To header:\n%s", mimeStr)
	}
}

func TestPersonalSending_OutboxWorker_ConnectionNeeded_And_Retry(t *testing.T) {
	t.Parallel()

	fake := modtest.NewFakePersonalMailbox()
	h := newHarness(t, modtest.WithPersonalMailbox(fake))
	channelID := setupChannel(t, h)
	client, userID := h.SignInUser(t, "communications:conversations-reply", "communications:conversations-view")

	fake.SetUser(userID, contracts.PersonalMailStatus{
		Available: true,
		Address:   "kari@firma.no",
		Mailbox:   "kari-oid",
	})

	body := map[string]any{
		"channelId":        channelID,
		"subject":          "Subject",
		"textBody":         "Hello",
		"sendFromPersonal": true,
		"to":               []map[string]any{{"email": "customer@example.test"}},
	}
	res := client.Do(http.MethodPost, "/api/v1/communications/conversations", body, modtest.Header("Idempotency-Key", uuid.NewString()))
	if res.Status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", res.Status, res.Body)
	}
	var mut conversationMutationJSON
	res.JSON(&mut)
	convID := mut.ConversationId
	msgID := uuid.MustParse(*mut.MessageId)

	// Personal mailbox send fails with ErrConnectionNeeded
	fake.FailWith("Send", contracts.ErrConnectionNeeded)

	w := communications.NewOutboxWorker(h.Deps())
	processed, err := w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if !processed {
		t.Fatal("ProcessOne = false, want true")
	}

	// Job should fail immediately after 1 attempt with terminal code connection_needed
	job := readOutboxJob(t, h, msgID)
	if job.status != "failed" {
		t.Errorf("job status = %q, want failed", job.status)
	}
	if job.attempts != 1 {
		t.Errorf("job attempts = %d, want 1", job.attempts)
	}

	deliveries := readDeliveries(t, h, msgID)
	if len(deliveries) != 1 || deliveries[0].status != "submission_failed" {
		t.Errorf("deliveries = %+v, want 1 submission_failed", deliveries)
	}

	// GetConversation should report error = connection_needed
	convRes := client.Do(http.MethodGet, "/api/v1/communications/conversations/"+convID, nil)
	if convRes.Status != http.StatusOK {
		t.Fatalf("get conversation status = %d, want 200", convRes.Status)
	}
	var detail conversationDetailJSON
	convRes.JSON(&detail)
	if len(detail.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(detail.Messages))
	}
	deliveriesJSON, ok := detail.Messages[0]["deliveries"].([]any)
	if !ok || len(deliveriesJSON) != 1 {
		t.Fatalf("message deliveries = %+v, want 1", detail.Messages[0]["deliveries"])
	}
	firstDelivery, ok := deliveriesJSON[0].(map[string]any)
	if !ok {
		t.Fatalf("first delivery = %+v", deliveriesJSON[0])
	}
	if firstDelivery["error"] != "connection_needed" {
		t.Errorf("delivery error = %v, want connection_needed", firstDelivery["error"])
	}

	// Non-author cannot retry (403)
	otherUser := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
	retryOther := otherUser.Do(http.MethodPost, fmt.Sprintf("/api/v1/communications/conversations/%s/messages/%s/retry", convID, msgID), nil)
	if retryOther.Status != http.StatusForbidden {
		t.Errorf("non-author retry status = %d, want 403; body: %s", retryOther.Status, retryOther.Body)
	}

	// Personal sender still unavailable -> 422 personal_sender_unavailable
	fake.SetUser(userID, contracts.PersonalMailStatus{
		Available: false,
		Reason:    "reconnect",
		ErrorCode: "consent_missing",
	})
	retryUnavail := client.Do(http.MethodPost, fmt.Sprintf("/api/v1/communications/conversations/%s/messages/%s/retry", convID, msgID), nil)
	if retryUnavail.Status != http.StatusUnprocessableEntity {
		t.Errorf("unavailable retry status = %d, want 422; body: %s", retryUnavail.Status, retryUnavail.Body)
	}
	if retryUnavail.Code() != "personal_sender_unavailable" {
		t.Errorf("code = %q, want personal_sender_unavailable", retryUnavail.Code())
	}

	// Once reconnected: personal sender available again
	fake.SetUser(userID, contracts.PersonalMailStatus{
		Available: true,
		Address:   "kari@firma.no",
		Mailbox:   "kari-oid",
	})
	fake.FailWith("Send", nil)

	// Author retries -> 200, status = queued
	retryRes := client.Do(http.MethodPost, fmt.Sprintf("/api/v1/communications/conversations/%s/messages/%s/retry", convID, msgID), nil)
	if retryRes.Status != http.StatusOK {
		t.Fatalf("retry status = %d, want 200; body: %s", retryRes.Status, retryRes.Body)
	}
	var retryMut conversationMutationJSON
	retryRes.JSON(&retryMut)
	if retryMut.Status != "queued" {
		t.Errorf("status = %q, want queued", retryMut.Status)
	}

	// Outbox job is pending again with 0 attempts
	jobAfter := readOutboxJob(t, h, msgID)
	if jobAfter.status != "pending" {
		t.Errorf("job status = %q, want pending", jobAfter.status)
	}
	if jobAfter.attempts != 0 {
		t.Errorf("job attempts = %d, want 0", jobAfter.attempts)
	}

	// Deliveries are queued again
	deliveriesAfter := readDeliveries(t, h, msgID)
	if len(deliveriesAfter) != 1 || deliveriesAfter[0].status != "queued" {
		t.Errorf("deliveries = %+v, want 1 queued", deliveriesAfter)
	}

	// Outbox worker now succeeds on delivery
	processed, err = w.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne after retry: %v", err)
	}
	if !processed {
		t.Fatal("ProcessOne after retry = false, want true")
	}

	jobFinal := readOutboxJob(t, h, msgID)
	if jobFinal.status != "completed" {
		t.Errorf("final job status = %q, want completed", jobFinal.status)
	}
}

func TestPersonalSending_Reply(t *testing.T) {
	t.Parallel()

	t.Run("409 connection_needed when personal sender unavailable on reply", func(t *testing.T) {
		t.Parallel()
		fake := modtest.NewFakePersonalMailbox()
		h := newHarness(t, modtest.WithPersonalMailbox(fake))
		client := h.SignIn(t, "communications:conversations-reply", "communications:conversations-view")
		convID, _ := replyableConversation(t, h, client)

		body := map[string]any{
			"textBody":         "Reply from personal",
			"sendFromPersonal": true,
		}
		res := client.Do(http.MethodPost, "/api/v1/communications/conversations/"+convID+"/reply", body, modtest.Header("Idempotency-Key", uuid.NewString()))
		if res.Status != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body: %s", res.Status, res.Body)
		}
		if res.Code() != "connection_needed" {
			t.Errorf("code = %q, want connection_needed", res.Code())
		}
	})

	t.Run("success stores send_as personal on reply", func(t *testing.T) {
		t.Parallel()
		fake := modtest.NewFakePersonalMailbox()
		h := newHarness(t, modtest.WithPersonalMailbox(fake))
		client, userID := h.SignInUser(t, "communications:conversations-reply", "communications:conversations-view")
		convID, _ := replyableConversation(t, h, client)

		fake.SetUser(userID, contracts.PersonalMailStatus{
			Available: true,
			Address:   "kari@firma.no",
			Mailbox:   "kari-oid",
		})

		body := map[string]any{
			"textBody":         "Reply from personal address",
			"sendFromPersonal": true,
		}
		res := client.Do(http.MethodPost, "/api/v1/communications/conversations/"+convID+"/reply", body, modtest.Header("Idempotency-Key", uuid.NewString()))
		if res.Status != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body: %s", res.Status, res.Body)
		}
		var mut conversationMutationJSON
		res.JSON(&mut)
		msgID := *mut.MessageId

		var sendAs string
		var fromAddress *string
		err := h.Pool().QueryRow(context.Background(),
			`SELECT send_as, from_address FROM communications.conversation_messages WHERE id = $1`, uuid.MustParse(msgID)).
			Scan(&sendAs, &fromAddress)
		if err != nil {
			t.Fatalf("query message: %v", err)
		}
		if sendAs != "personal" {
			t.Errorf("send_as = %q, want personal", sendAs)
		}
		if fromAddress == nil || *fromAddress != "kari@firma.no" {
			t.Errorf("from_address = %v, want kari@firma.no", fromAddress)
		}
	})
}
