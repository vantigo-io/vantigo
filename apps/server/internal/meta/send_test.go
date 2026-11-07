package meta_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/meta/metatest"
)

func grPart(text string) contracts.MessengerPart {
	return contracts.MessengerPart{MessageID: "msg-1", Participant: grPSID, Text: text}
}

func TestGraphSend_Text(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	sent, err := e.m.Send(context.Background(), grPageID, grPart("Hei, hvordan kan vi hjelpe?"))
	if err != nil || sent.PlatformMessageID != "m_1" || !sent.At.IsZero() {
		t.Fatalf("sent: %+v %v", sent, err)
	}
	got := e.fake.Sent()
	if len(got) != 1 {
		t.Fatalf("sent: %+v", got)
	}
	s := got[0]
	if s.PageID != grPageID || s.Recipient != grPSID || s.MessagingType != "RESPONSE" || s.Tag != "" || s.Text != "Hei, hvordan kan vi hjelpe?" {
		t.Fatalf("message: %+v", s)
	}
	if _, ok := s.Message["quick_replies"]; ok {
		t.Fatal("quick replies on a plain text")
	}
}

func TestGraphSend_QuickRepliesAndTheEmailRequest(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	part := grPart("Velg")
	part.AnswerOptions = []contracts.MessengerChoice{{ID: "yes", Title: "Ja"}, {ID: "no", Title: "Nei"}}
	part.ShareEmail = true
	if _, err := e.m.Send(context.Background(), grPageID, part); err != nil {
		t.Fatal(err)
	}
	replies, _ := e.fake.Sent()[0].Message["quick_replies"].([]any)
	if len(replies) != 3 {
		t.Fatalf("quick_replies: %+v", replies)
	}
	first := replies[0].(map[string]any)
	if first["content_type"] != "text" || first["title"] != "Ja" || first["payload"] != contracts.MessengerPayload("msg-1", "yes") {
		t.Fatalf("first reply: %+v", first)
	}
	if last := replies[2].(map[string]any); last["content_type"] != "user_email" {
		t.Fatalf("last reply: %+v", last)
	}
}

func TestGraphSend_HumanAgentIsTagged(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	part := grPart("Sent etter vinduet")
	part.HumanAgent = true
	if _, err := e.m.Send(context.Background(), grPageID, part); err != nil {
		t.Fatal(err)
	}
	if s := e.fake.Sent()[0]; s.MessagingType != "MESSAGE_TAG" || s.Tag != "HUMAN_AGENT" {
		t.Fatalf("message: %+v", s)
	}
}

func TestGraphSend_AttachmentsGoAsMultipart(t *testing.T) {
	for _, tc := range []struct{ contentType, wantType string }{
		{"image/png", "image"}, {"audio/mpeg", "audio"}, {"video/mp4", "video"},
		{"application/pdf", "file"}, {"", "file"},
	} {
		t.Run(tc.wantType+tc.contentType, func(t *testing.T) {
			e := newGrEnv(t).withPage(t)
			data := []byte("\x89PNG\r\n\x1a\n binary \x00\x01\xff")
			part := contracts.MessengerPart{
				MessageID: "msg-2", Participant: grPSID, HumanAgent: true,
				Attachment:    &contracts.MessengerAttachment{FileName: "bilde.png", ContentType: tc.contentType, Content: data},
				AnswerOptions: []contracts.MessengerChoice{{ID: "ok", Title: "OK"}},
			}
			sent, err := e.m.Send(context.Background(), grPageID, part)
			if err != nil || sent.PlatformMessageID != "m_1" {
				t.Fatalf("sent: %+v %v", sent, err)
			}
			s := e.fake.Sent()[0]
			if !bytes.Equal(s.FileData, data) || s.FileName != "bilde.png" {
				t.Fatalf("file: %q %q", s.FileData, s.FileName)
			}
			wantCT := tc.contentType
			if wantCT == "" {
				wantCT = "application/octet-stream"
			}
			if s.FileContentType != wantCT {
				t.Fatalf("content type %q, want %q", s.FileContentType, wantCT)
			}
			att, _ := s.Message["attachment"].(map[string]any)
			payload, _ := att["payload"].(map[string]any)
			if att["type"] != tc.wantType || payload["is_reusable"] != false {
				t.Fatalf("attachment: %+v", att)
			}
			if s.Recipient != grPSID || s.MessagingType != "MESSAGE_TAG" || s.Tag != "HUMAN_AGENT" {
				t.Fatalf("message: %+v", s)
			}
			if replies, _ := s.Message["quick_replies"].([]any); len(replies) != 1 {
				t.Fatalf("quick_replies: %+v", s.Message["quick_replies"])
			}
		})
	}
}

func TestGraphSend_RefusesWhatMetaWouldNotTakeWithoutACall(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	ctx := context.Background()
	e.fake.ResetCalls()

	if _, err := e.m.Send(ctx, grPageID, grPart(strings.Repeat("å", 2000))); err != nil {
		t.Fatalf("2000 characters: %v", err)
	}
	e.fake.ResetCalls()

	long := grPart(strings.Repeat("å", 2001))
	cards := grPart("")
	cards.Cards = []contracts.MessengerCard{{Title: "Kort", ImageURL: "http://plain.example/x.png"}}
	manyChoices := grPart("")
	manyChoices.Cards = []contracts.MessengerCard{{Title: "Kort", Choices: []contracts.MessengerChoice{
		{ID: "a", Title: "A"}, {ID: "b", Title: "B"}, {ID: "c", Title: "C"}, {ID: "d", Title: "D"}}}}
	eleven := grPart("")
	for i := 0; i < 11; i++ {
		eleven.Cards = append(eleven.Cards, contracts.MessengerCard{Title: "Kort"})
	}
	big := contracts.MessengerPart{Participant: grPSID, Attachment: &contracts.MessengerAttachment{
		FileName: "stor.bin", ContentType: "application/octet-stream", Content: make([]byte, 25<<20+1),
	}}
	empty := grPart("")
	for name, part := range map[string]contracts.MessengerPart{"too long": long, "a plain image": cards, "four choices": manyChoices, "eleven cards": eleven, "too big": big, "nothing": empty} {
		_, err := e.m.Send(ctx, grPageID, part)
		if !errors.Is(err, contracts.ErrMessengerRejected) || contracts.IsMessengerTemporary(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if n := len(e.fake.Calls()); n != 0 {
		t.Fatalf("%d calls were made", n)
	}
}

func TestGraphSend_HoldsWhenThePageCannotSend(t *testing.T) {
	ctx := context.Background()
	e := newGrEnv(t).withPage(t)
	e.fake.ResetCalls()
	if _, err := e.m.Send(ctx, "404", grPart("hei")); !errors.Is(err, contracts.ErrMessengerUnavailable) {
		t.Fatalf("unknown Page: %v", err)
	}
	e.h.Exec(t, `UPDATE meta.pages SET state = 'token_rejected' WHERE page_id = $1`, grPageID)
	if _, err := e.m.Send(ctx, grPageID, grPart("hei")); !errors.Is(err, contracts.ErrMessengerUnavailable) {
		t.Fatalf("rejected Page: %v", err)
	}
	if n := len(e.fake.Calls()); n != 0 {
		t.Fatalf("%d calls were made", n)
	}

	un := newGrEnvUnconfigured(t)
	if _, err := un.m.Send(ctx, grPageID, grPart("hei")); !errors.Is(err, contracts.ErrMessengerUnavailable) {
		t.Fatalf("unconfigured: %v", err)
	}
}

// TestGraphSend_TheErrorMap runs every row of Messenger design N12's table
// against the fake, with its side effects.
func TestGraphSend_TheErrorMap(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failure   metatest.Failure
		tagged    bool
		temporary bool
		is        error
		recorded  string
		rejected  bool
	}{
		{name: "code 1", failure: metatest.Failure{Status: 500, Code: 1, Message: "unknown"}, temporary: true},
		{name: "code 2", failure: metatest.Failure{Status: 503, Code: 2, Message: "down"}, temporary: true},
		{name: "code 4", failure: metatest.Failure{Status: 400, Code: 4, Message: "limit"}, temporary: true},
		{name: "code 613", failure: metatest.Failure{Status: 400, Code: 613, Message: "rate"}, temporary: true},
		{name: "a 502", failure: metatest.Failure{Status: 502}, temporary: true},
		{name: "a network error", failure: metatest.Failure{Network: true}, temporary: true},
		{name: "551", failure: metatest.Failure{Status: 400, Code: 551, Message: "blocked"}, is: contracts.ErrRecipientUnreachable, recorded: "551"},
		{name: "10/2018278", failure: metatest.Failure{Status: 400, Code: 10, Subcode: 2018278, Message: "window"}, is: contracts.ErrReplyWindowClosed, recorded: "10/2018278"},
		{name: "190", failure: metatest.Failure{Status: 400, Code: 190, Message: "token"}, is: contracts.ErrMessengerRejected, recorded: "190", rejected: true},
		{name: "10 tagged", failure: metatest.Failure{Status: 403, Code: 10, Message: "perm"}, tagged: true, is: contracts.ErrHumanAgentNotPermitted, recorded: "human_agent_not_permitted"},
		{name: "200 tagged", failure: metatest.Failure{Status: 403, Code: 200, Message: "perm"}, tagged: true, is: contracts.ErrHumanAgentNotPermitted, recorded: "human_agent_not_permitted"},
		{name: "10 untagged", failure: metatest.Failure{Status: 403, Code: 10, Message: "perm"}, is: contracts.ErrMessengerRejected, recorded: "10"},
		{name: "200 untagged", failure: metatest.Failure{Status: 403, Code: 200, Message: "perm"}, is: contracts.ErrMessengerRejected, recorded: "200"},
		{name: "anything else", failure: metatest.Failure{Status: 400, Code: 100, Message: "Invalid parameter"}, is: contracts.ErrMessengerRejected, recorded: "100"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newGrEnv(t).withPage(t)
			e.fake.FailNext("POST /"+grPageID+"/messages", tc.failure)
			part := grPart("hei")
			part.HumanAgent = tc.tagged
			_, err := e.m.Send(context.Background(), grPageID, part)
			if err == nil {
				t.Fatal("no error")
			}
			if contracts.IsMessengerTemporary(err) != tc.temporary {
				t.Fatalf("temporary = %v: %v", !tc.temporary, err)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("err = %v, want %v", err, tc.is)
			}
			if errors.Is(tc.is, contracts.ErrMessengerRejected) && !strings.Contains(err.Error(), tc.failure.Message) {
				t.Fatalf("Meta's message is lost: %v", err)
			}
			if got := grLastCode(t, e); got != tc.recorded {
				t.Fatalf("recorded code %q, want %q", got, tc.recorded)
			}
			wantState := "active"
			if tc.rejected {
				wantState = "token_rejected"
			}
			if got := grPageState(t, e); got != wantState {
				t.Fatalf("Page state %q, want %q", got, wantState)
			}
			if tc.rejected {
				e.fake.ResetCalls()
				if _, err := e.m.Send(context.Background(), grPageID, grPart("igjen")); !errors.Is(err, contracts.ErrMessengerUnavailable) {
					t.Fatalf("a send after the rejection: %v", err)
				}
				if len(e.fake.Calls()) != 0 {
					t.Fatal("a rejected Page was called")
				}
			}
			for _, secret := range []string{grPageToken, grPSID} {
				if strings.Contains(e.h.Logs(), secret) {
					t.Errorf("the logs hold %q", secret)
				}
			}
		})
	}
}

func TestGraphSend_CardsGoAsAGenericTemplate(t *testing.T) {
	e := newGrEnv(t).withPage(t)
	part := grPart("")
	part.MessageID = "msg-9"
	part.AnswerOptions = []contracts.MessengerChoice{{ID: "later", Title: "Later"}}
	part.Cards = []contracts.MessengerCard{
		{Title: "Stol", Subtitle: "1 200 kr", ImageURL: "https://pay.example.com/api/v1/communications/public-media/a.b.c",
			Choices: []contracts.MessengerChoice{{ID: "buy", Title: "Kjøp"}, {ID: "more", Title: "Mer"}}},
		{Title: "Bord"},
	}
	sent, err := e.m.Send(context.Background(), grPageID, part)
	if err != nil || sent.PlatformMessageID != "m_1" {
		t.Fatalf("sent: %+v %v", sent, err)
	}
	s := e.fake.Sent()[0]
	att, _ := s.Message["attachment"].(map[string]any)
	payload, _ := att["payload"].(map[string]any)
	elements, _ := payload["elements"].([]any)
	if att["type"] != "template" || payload["template_type"] != "generic" || len(elements) != 2 {
		t.Fatalf("attachment: %+v", att)
	}
	first := elements[0].(map[string]any)
	if first["title"] != "Stol" || first["subtitle"] != "1 200 kr" || first["image_url"] != "https://pay.example.com/api/v1/communications/public-media/a.b.c" {
		t.Fatalf("first element: %+v", first)
	}
	buttons, _ := first["buttons"].([]any)
	if len(buttons) != 2 {
		t.Fatalf("buttons: %+v", buttons)
	}
	b := buttons[0].(map[string]any)
	if b["type"] != "postback" || b["title"] != "Kjøp" || b["payload"] != contracts.MessengerPayload("msg-9", "buy") {
		t.Fatalf("first button: %+v", b)
	}
	second := elements[1].(map[string]any)
	if _, ok := second["buttons"]; ok || second["image_url"] != nil {
		t.Fatalf("second element: %+v", second)
	}
	if replies, _ := s.Message["quick_replies"].([]any); len(replies) != 1 {
		t.Fatalf("quick_replies: %+v", s.Message["quick_replies"])
	}
	if s.MessagingType != "RESPONSE" {
		t.Fatalf("messaging type %q", s.MessagingType)
	}
}
