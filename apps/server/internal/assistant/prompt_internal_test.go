package assistant

import (
	"strings"
	"testing"
)

// The prompt names Knowledge as the place for the company's own routines and
// asks the model to cite the article it answers from, by its in-app route
// (knowledge design K15).
func TestSystemPrompt_NamesKnowledgeAndAsksForACitation(t *testing.T) {
	p := systemPrompt(promptInput{name: "Kari", language: "nb"})
	for _, want := range []string{
		"Knowledge holds the company's own routines",
		"searchKnowledge",
		"getKnowledgeArticleText",
		"/knowledge/articles/{id}",
		"- /knowledge/articles/{articleId}: a Knowledge article",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt does not say %q", want)
		}
	}
}
