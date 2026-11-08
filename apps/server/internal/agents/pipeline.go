package agents

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// This file is the outbound pipeline of a reply (virtual employees design V11,
// V17, V18, V19): what the code decides after the model has written, before a
// reply is sent or waits for a person. Everything here is pure, so each rule is
// a table test. The persona and the instructions are prompt; these rules are
// code, and nothing the model or a customer wrote can change them.

// The rules of V18 by their codes, and the pseudo-rule of a failed content check.
const (
	ruleS1    = "S1" // recipient and the customer's data
	ruleS2    = "S2" // another customer's data, attachments
	ruleS3    = "S3" // no source
	ruleS4    = "S4" // first contact
	ruleS5    = "S5" // complaint
	ruleS6    = "S6" // reply window
	ruleS7    = "S7" // loop guard
	ruleC1    = "C1" // claims to be a person
	ruleC2    = "C2" // a promise the instructions do not allow
	ruleCheck = "CHECK"
	// ruleChange holds a change in Review in this delivery (V19).
	ruleChange = "CHANGE"
)

// maxRunsPerHour is S7's limit of runs on one conversation in an hour (V23).
const maxRunsPerHour = 6

// The words an action log gives an operation that reads the conversation itself:
// what the sender wrote is never evidence for a reply (V17), and no more is a note
// an earlier run kept about the customer (V27).
var ownConversationOperations = []string{"getCommunicationsConversationsById", "getCommunicationsConversations", memoryOperation}

// action is one row of the run's action log, as the pipeline reads it.
type action struct {
	Operation string
	Ref       string
	Scoped    bool
	OK        bool
}

// values are the ids an action's ref names after the operation.
func (a action) values() []string {
	f := strings.Fields(a.Ref)
	if len(f) > 0 && f[0] == a.Operation {
		f = f[1:]
	}
	return f
}

// validSources keeps the sources the run's action log shows it read, and drops
// the rest and the conversation's own messages (V17). It answers the kept ones
// as given, in order, and whether one of them is a read of a customer's data.
func validSources(sources []string, log []action) (valid []string, scoped bool) {
	for _, src := range sources {
		src = strings.TrimSpace(src)
		if src == "" || slices.Contains(valid, src) {
			continue
		}
		for _, a := range log {
			if !a.OK || slices.Contains(ownConversationOperations, a.Operation) {
				continue
			}
			if !sourceMatches(src, a) {
				continue
			}
			valid = append(valid, src)
			scoped = scoped || a.Scoped
			break
		}
	}
	return valid, scoped
}

// runHasScopedRead says the run read a customer's data: any successful read of
// an operation that returns it, whatever the reply cites (S1).
func runHasScopedRead(log []action) bool {
	return slices.ContainsFunc(log, func(a action) bool { return a.OK && a.Scoped })
}

// sourceMatches says src names a: its reference as written, or one of the ids it
// read (an article id, an invoice number).
func sourceMatches(src string, a action) bool {
	if strings.EqualFold(strings.Join(strings.Fields(src), " "), strings.Join(strings.Fields(a.Ref), " ")) {
		return true
	}
	if strings.ContainsAny(src, " \t") {
		return false
	}
	return slices.ContainsFunc(a.values(), func(v string) bool { return strings.EqualFold(v, src) })
}

// scoreOf is V17's score: 0 with no valid source, else the model's own, kept
// between 0 and 100.
func scoreOf(modelScore int, valid []string) int {
	if len(valid) == 0 {
		return 0
	}
	return min(max(modelScore, 0), 100)
}

// replyFacts is what the rules know of the run and the conversation.
type replyFacts struct {
	// Recipient is the latest inbound sender's own address, "" when there is none.
	Recipient string
	// CustomerID is the run's customer scope; HasCustomer says it is set.
	HasCustomer bool
	// RecipientIsContact says the recipient is a contact address of the run's
	// customer, or the Messenger participant linked to one of its contacts.
	RecipientIsContact bool
	// FirstContact is the conversation's first inbound message from a sender
	// with no earlier conversation (S4).
	FirstContact bool
	// ComplaintTagged says the conversation carries a tag the role marks (S5).
	ComplaintTagged bool
	// WindowState is a Messenger conversation's reply window, "" for a channel
	// with none (S6).
	WindowState string
	// AutoReply says the latest inbound message is an automatic one (S7).
	AutoReply bool
	// SenderIsChannel says the latest inbound sender is a channel address of this
	// installation (S7).
	SenderIsChannel bool
	// RepliesToday is the agent messages sent in the conversation in the last
	// day; RunsLastHour the other runs started on it in the last hour (S7).
	RepliesToday int
	RunsLastHour int
}

// check is the content check's answer (C1, C2): ok false when the call failed or
// timed out, so the item is held.
type check struct {
	OK          bool
	ClaimsHuman bool
	BadPromise  bool
}

// ruleInput is everything decideReply weighs.
type ruleInput struct {
	Mode             string
	Threshold        int
	MaxRepliesPerDay int
	Score            int
	// CarriesCustomerData says a valid source is a read of a customer's data.
	CarriesCustomerData bool
	// Attachments is how many Vantigo documents the reply attaches, and
	// AttachmentsOwned that every one belongs to the run's customer.
	Attachments      int
	AttachmentsOwned bool
	Facts            replyFacts
	Check            check
}

// decision is what the pipeline decides for a reply.
type decision struct {
	// Refused are the rules that stop the reply for good (S6, S7): it is not
	// sent and not queued, and the conversation is handed off.
	Refused []string
	// Held are the rules that hold it for a person.
	Held []string
	// Send says the reply goes out now: the mode lets it and nothing held it.
	Send bool
}

// decideReply applies V18 and V19. S6 and S7 hold in every mode and stop the
// reply; S1, S2 and C1 hold in every mode, Auto too; S3, S4, S5 and C2 hold in
// Review and Auto above X % only. In Review the rules are still named, so the
// reviewer sees why the item would not have gone by itself; the mode decides
// whether anything is sent.
func decideReply(in ruleInput) decision {
	var d decision
	f := in.Facts

	// S6: only an open window; the human-agent extension is never an agent's (V22).
	if f.WindowState == "closed" || f.WindowState == "human_agent" {
		d.Refused = append(d.Refused, ruleS6)
	}
	// S7: the loop guards (V23).
	if f.AutoReply || f.SenderIsChannel || f.RepliesToday >= in.MaxRepliesPerDay || f.RunsLastHour >= maxRunsPerHour {
		d.Refused = append(d.Refused, ruleS7)
	}
	if len(d.Refused) > 0 {
		return d
	}

	// S1: the reply goes to the latest inbound sender alone; with a customer's data
	// in it, that sender must be the customer's contact.
	carriesCustomerData := in.CarriesCustomerData || in.Attachments > 0
	if f.Recipient == "" || (carriesCustomerData && (!f.HasCustomer || !f.RecipientIsContact)) {
		d.Held = append(d.Held, ruleS1)
	}
	// S2: an attachment is the run's customer's, and a run with none attaches nothing.
	if in.Attachments > 0 && (!f.HasCustomer || !in.AttachmentsOwned) {
		d.Held = append(d.Held, ruleS2)
	}
	if in.Score == 0 {
		d.Held = append(d.Held, ruleS3)
	}
	if f.FirstContact {
		d.Held = append(d.Held, ruleS4)
	}
	if f.ComplaintTagged {
		d.Held = append(d.Held, ruleS5)
	}
	switch {
	case !in.Check.OK:
		d.Held = append(d.Held, ruleCheck)
	default:
		if in.Check.ClaimsHuman {
			d.Held = append(d.Held, ruleC1)
		}
		if in.Check.BadPromise {
			d.Held = append(d.Held, ruleC2)
		}
	}

	switch in.Mode {
	case modeAuto:
		// Only the locked rules apply in Auto: S3, S4, S5 and C2 are the other
		// modes' (V19).
		d.Held = slices.DeleteFunc(d.Held, func(code string) bool { return !lockedRule(code) })
		d.Send = len(d.Held) == 0
	case modeAutoAbove:
		d.Send = len(d.Held) == 0 && in.Score >= in.Threshold
	}
	return d
}

// lockedRule says a rule holds in every mode, Auto too (S1, S2, C1, and a check
// that did not answer).
func lockedRule(code string) bool {
	return code == ruleS1 || code == ruleS2 || code == ruleC1 || code == ruleCheck
}

// disclosureLine is the opening line of every agent message (V11): who the
// customer talks to, that it is an AI, and for which company. The names sit in
// guillemets. Compact is the same Norwegian-first wording in both languages. It
// mirrors apps/agents/frontend/src/lib/disclosure.ts, which previews it.
func disclosureLine(wording, language, agentName, company string) string {
	a, c := "«"+agentName+"»", "«"+company+"»"
	if wording == "compact" {
		return fmt.Sprintf("%s (KI) · virtuell assistent, %s.", a, c)
	}
	if language == "nb" {
		if wording == "assistant" {
			return fmt.Sprintf("%s er %s sin virtuelle assistent (KI).", a, c)
		}
		return fmt.Sprintf("Du snakker med %s, en KI-assistent hos %s.", a, c)
	}
	if wording == "assistant" {
		return fmt.Sprintf("%s is %s's virtual assistant (AI).", a, c)
	}
	return fmt.Sprintf("You are talking to %s, an AI assistant at %s.", a, c)
}

// disclosureLanguage is the language the disclosure is worded in: the persona's
// own, or, for one that follows the customer, the language the reply was written
// in (bokmål unless it is plainly English).
func disclosureLanguage(languages, text string) string {
	if languages == "nb" || languages == "en" {
		return languages
	}
	if looksEnglish(text) {
		return "en"
	}
	return "nb"
}

var englishWords = map[string]bool{
	"the": true, "and": true, "you": true, "your": true, "is": true, "are": true, "we": true, "of": true, "to": true,
	"for": true, "with": true, "this": true, "that": true, "please": true, "thank": true, "thanks": true, "have": true,
	"will": true, "can": true, "our": true, "order": true, "from": true, "hello": true, "hi": true, "not": true,
}

var norwegianWords = map[string]bool{
	"og": true, "du": true, "er": true, "vi": true, "det": true, "som": true, "til": true, "med": true,
	"en": true, "et": true, "ikke": true, "har": true, "kan": true, "din": true, "ditt": true, "dine": true, "hei": true,
	"takk": true, "bestilling": true, "fra": true, "på": true, "jeg": true, "vil": true, "vår": true, "våre": true,
}

// looksEnglish counts the common English and Norwegian words of text and says
// English has more of them.
func looksEnglish(text string) bool {
	en, nb := 0, 0
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if englishWords[w] {
			en++
		}
		if norwegianWords[w] {
			nb++
		}
	}
	return en > nb
}

// withDisclosure puts the disclosure line at the top of an agent's message.
func withDisclosure(line, text string) string {
	return line + "\n\n" + strings.TrimSpace(text)
}
