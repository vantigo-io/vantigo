package agents

import (
	"slices"
	"strings"
	"testing"
)

// The outbound pipeline, rule by rule (virtual employees design V17, V18, V19).

func goodCheck() check { return check{OK: true} }

// base is a reply nothing holds: a score of 90, a recipient, an open channel.
func base(mode string) ruleInput {
	return ruleInput{
		Mode: mode, Threshold: 85, MaxRepliesPerDay: 5, Score: 90, Check: goodCheck(),
		Facts: replyFacts{Recipient: "kari@example.test", HasCustomer: true},
	}
}

func TestValidSources_OnlyWhatTheRunReadAndNeverTheConversation(t *testing.T) {
	t.Parallel()
	log := []action{
		{Operation: "getInvoicesById", Ref: "getInvoicesById 2026-0931", Scoped: true, OK: true},
		{Operation: "getKnowledgeArticleText", Ref: "getKnowledgeArticleText 7c1e", OK: true},
		{Operation: "getCommunicationsConversationsById", Ref: "getCommunicationsConversationsById 11", OK: true},
		{Operation: "getSalesOrder", Ref: "getSalesOrder 55", Scoped: true, OK: false},
	}
	tests := []struct {
		name       string
		sources    []string
		want       []string
		wantScoped bool
	}{
		{"a read as written", []string{"getInvoicesById 2026-0931"}, []string{"getInvoicesById 2026-0931"}, true},
		{"case and spacing do not matter", []string{"getinvoicesbyid   2026-0931"}, []string{"getinvoicesbyid   2026-0931"}, true},
		{"an id alone names the read", []string{"7c1e"}, []string{"7c1e"}, false},
		{"a read the run did not make is dropped", []string{"getInvoicesById 2026-0932"}, nil, false},
		{"the conversation's own messages are never evidence", []string{"getCommunicationsConversationsById 11", "11"}, nil, false},
		{"a failed read is not evidence", []string{"getSalesOrder 55", "55"}, nil, false},
		{"an invented source is dropped and a real one kept", []string{"the customer said so", "7c1e"}, []string{"7c1e"}, false},
		{"duplicates count once", []string{"7c1e", "7c1e"}, []string{"7c1e"}, false},
		{"nothing cited", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, scoped := validSources(tt.sources, log)
			if !slices.Equal(got, tt.want) || scoped != tt.wantScoped {
				t.Errorf("validSources = %v, scoped %v; want %v, scoped %v", got, scoped, tt.want, tt.wantScoped)
			}
		})
	}
}

func TestScore_NoValidSourceIsZeroAndTheModelsOwnIsKeptInRange(t *testing.T) {
	t.Parallel()
	if got := scoreOf(97, nil); got != 0 {
		t.Errorf("no source: score %d, want 0", got)
	}
	if got := scoreOf(97, []string{"x"}); got != 97 {
		t.Errorf("a source: score %d, want 97", got)
	}
	if got := scoreOf(140, []string{"x"}); got != 100 {
		t.Errorf("over range: score %d, want 100", got)
	}
	if got := scoreOf(-5, []string{"x"}); got != 0 {
		t.Errorf("under range: score %d, want 0", got)
	}
}

// TestDecide_TheRulesByMode is the table of V18 and V19: for each rule, whether
// it refuses, holds, or leaves the reply to go, in each mode.
func TestDecide_TheRulesByMode(t *testing.T) {
	t.Parallel()
	type result struct {
		refused, held string // the rule codes, joined; "" for none
		send          bool
	}
	tests := []struct {
		name   string
		change func(in *ruleInput)
		review result
		above  result
		auto   result
	}{
		{"nothing wrong", func(*ruleInput) {}, result{"", "", false}, result{"", "", true}, result{"", "", true}},
		{"S1 customer data to a sender who is not the customer's contact",
			func(in *ruleInput) { in.CarriesCustomerData = true },
			result{"", "S1", false}, result{"", "S1", false}, result{"", "S1", false}},
		{"S1 customer data to the customer's own contact",
			func(in *ruleInput) { in.CarriesCustomerData = true; in.Facts.RecipientIsContact = true },
			result{"", "", false}, result{"", "", true}, result{"", "", true}},
		{"S1 customer data and the run has no customer",
			func(in *ruleInput) {
				in.CarriesCustomerData = true
				in.Facts.HasCustomer = false
				in.Facts.RecipientIsContact = true
			},
			result{"", "S1", false}, result{"", "S1", false}, result{"", "S1", false}},
		{"S1 no recipient",
			func(in *ruleInput) { in.Facts.Recipient = "" },
			result{"", "S1", false}, result{"", "S1", false}, result{"", "S1", false}},
		{"S2 an attachment with no customer",
			func(in *ruleInput) { in.Attachments = 1; in.AttachmentsOwned = true; in.Facts.HasCustomer = false },
			result{"", "S1,S2", false}, result{"", "S1,S2", false}, result{"", "S1,S2", false}},
		{"S2 an attachment that is not the customer's",
			func(in *ruleInput) { in.Attachments = 1; in.Facts.RecipientIsContact = true },
			result{"", "S2", false}, result{"", "S2", false}, result{"", "S2", false}},
		{"S3 no source",
			func(in *ruleInput) { in.Score = 0 },
			result{"", "S3", false}, result{"", "S3", false}, result{"", "", true}},
		{"S4 first contact",
			func(in *ruleInput) { in.Facts.FirstContact = true },
			result{"", "S4", false}, result{"", "S4", false}, result{"", "", true}},
		{"S5 a complaint",
			func(in *ruleInput) { in.Facts.ComplaintTagged = true },
			result{"", "S5", false}, result{"", "S5", false}, result{"", "", true}},
		{"S6 the window is closed",
			func(in *ruleInput) { in.Facts.WindowState = "closed" },
			result{"S6", "", false}, result{"S6", "", false}, result{"S6", "", false}},
		{"S6 only the human-agent window is left",
			func(in *ruleInput) { in.Facts.WindowState = "human_agent" },
			result{"S6", "", false}, result{"S6", "", false}, result{"S6", "", false}},
		{"S6 an open window",
			func(in *ruleInput) { in.Facts.WindowState = "open" },
			result{"", "", false}, result{"", "", true}, result{"", "", true}},
		{"S7 an auto-reply",
			func(in *ruleInput) { in.Facts.AutoReply = true },
			result{"S7", "", false}, result{"S7", "", false}, result{"S7", "", false}},
		{"S7 another channel of this installation",
			func(in *ruleInput) { in.Facts.SenderIsChannel = true },
			result{"S7", "", false}, result{"S7", "", false}, result{"S7", "", false}},
		{"S7 the day's limit of replies",
			func(in *ruleInput) { in.Facts.RepliesToday = 5 },
			result{"S7", "", false}, result{"S7", "", false}, result{"S7", "", false}},
		{"S7 below the day's limit",
			func(in *ruleInput) { in.Facts.RepliesToday = 4 },
			result{"", "", false}, result{"", "", true}, result{"", "", true}},
		{"S7 six runs in the hour",
			func(in *ruleInput) { in.Facts.RunsLastHour = 6 },
			result{"S7", "", false}, result{"S7", "", false}, result{"S7", "", false}},
		{"S7 five runs in the hour",
			func(in *ruleInput) { in.Facts.RunsLastHour = 5 },
			result{"", "", false}, result{"", "", true}, result{"", "", true}},
		{"C1 claims to be a person",
			func(in *ruleInput) { in.Check = check{OK: true, ClaimsHuman: true} },
			result{"", "C1", false}, result{"", "C1", false}, result{"", "C1", false}},
		{"C2 a promise the instructions do not allow",
			func(in *ruleInput) { in.Check = check{OK: true, BadPromise: true} },
			result{"", "C2", false}, result{"", "C2", false}, result{"", "", true}},
		{"the check failed or timed out",
			func(in *ruleInput) { in.Check = check{} },
			result{"", "CHECK", false}, result{"", "CHECK", false}, result{"", "CHECK", false}},
		{"a score under the threshold",
			func(in *ruleInput) { in.Score = 84 },
			result{"", "", false}, result{"", "", false}, result{"", "", true}},
		{"a score at the threshold",
			func(in *ruleInput) { in.Score = 85 },
			result{"", "", false}, result{"", "", true}, result{"", "", true}},
	}
	for _, tt := range tests {
		for mode, want := range map[string]result{modeReview: tt.review, modeAutoAbove: tt.above, modeAuto: tt.auto} {
			t.Run(tt.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				in := base(mode)
				tt.change(&in)
				d := decideReply(in)
				got := result{strings.Join(d.Refused, ","), strings.Join(d.Held, ","), d.Send}
				if got != want {
					t.Errorf("decideReply = %+v, want %+v", got, want)
				}
			})
		}
	}
}

func TestDecide_ARefusalOutranksEveryMode(t *testing.T) {
	t.Parallel()
	in := base(modeAuto)
	in.Facts.WindowState = "closed"
	in.CarriesCustomerData = true // S1 would also hold it
	d := decideReply(in)
	if d.Send || len(d.Refused) != 1 || d.Refused[0] != ruleS6 || len(d.Held) != 0 {
		t.Errorf("decision = %+v, want S6 alone and nothing sent", d)
	}
}

func TestDisclosure_WordingLanguageAndPlacement(t *testing.T) {
	t.Parallel()
	tests := []struct{ wording, lang, want string }{
		{"assistant", "nb", "«Hanna» er «Hansen Rør» sin virtuelle assistent (KI)."},
		{"assistant", "en", "«Hanna» is «Hansen Rør»'s virtual assistant (AI)."},
		{"talking", "nb", "Du snakker med «Hanna», en KI-assistent hos «Hansen Rør»."},
		{"talking", "en", "You are talking to «Hanna», an AI assistant at «Hansen Rør»."},
		{"compact", "nb", "«Hanna» (KI) · virtuell assistent, «Hansen Rør»."},
		{"compact", "en", "«Hanna» (KI) · virtuell assistent, «Hansen Rør»."},
	}
	for _, tt := range tests {
		if got := disclosureLine(tt.wording, tt.lang, "Hanna", "Hansen Rør"); got != tt.want {
			t.Errorf("disclosureLine(%s, %s) = %q, want %q", tt.wording, tt.lang, got, tt.want)
		}
	}
	if got := withDisclosure("LINE", "  Hei!  "); got != "LINE\n\nHei!" {
		t.Errorf("withDisclosure = %q, want the line at the top", got)
	}
	if got := disclosureLanguage("nb", "Thank you for your order"); got != "nb" {
		t.Errorf("a persona fixed to nb: language %s", got)
	}
	if got := disclosureLanguage("customer", "Thank you, we will send your order to you."); got != "en" {
		t.Errorf("English reply, customer rule: language %s, want en", got)
	}
	if got := disclosureLanguage("customer", "Takk for henvendelsen. Vi sender bestillingen din i dag."); got != "nb" {
		t.Errorf("Norwegian reply, customer rule: language %s, want nb", got)
	}
}

func TestParseCheck_TheRubricAndNothingElse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		answer string
		want   check
		ok     bool
	}{
		{"clean", `{"claimsHuman": false, "promises": []}`, check{OK: true}, true},
		{"claims to be human", `{"claimsHuman": true, "promises": []}`, check{OK: true, ClaimsHuman: true}, true},
		{"an allowed promise", `{"claimsHuman": false, "promises": [{"text": "free shipping", "allowed": true}]}`, check{OK: true}, true},
		{"a promise not allowed", `{"claimsHuman": false, "promises": [{"text": "10% off", "allowed": false}]}`, check{OK: true, BadPromise: true}, true},
		{"text around the object", "Here you go:\n```json\n{\"claimsHuman\": false, \"promises\": []}\n```", check{OK: true}, true},
		{"a promise with no verdict counts as not allowed", `{"claimsHuman": false, "promises": [{"text": "tomorrow"}]}`, check{OK: true, BadPromise: true}, true},
		{"no claimsHuman is no answer", `{"promises": []}`, check{}, false},
		{"not json", "I think it is fine.", check{}, false},
		{"empty", "", check{}, false},
	}
	for _, tt := range tests {
		got, ok := parseCheck(tt.answer)
		if got != tt.want || ok != tt.ok {
			t.Errorf("%s: parseCheck = %+v, %v; want %+v, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}
