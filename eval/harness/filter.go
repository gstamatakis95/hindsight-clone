package harness

// Filter narrows a dataset for a quicker run. Zero fields keep everything.
type Filter struct {
	// Categories keeps only questions in these categories; conversations left without questions are dropped.
	Categories []string
	// MaxConversations keeps the first n conversations (after category filtering).
	MaxConversations int
	// MaxQuestions keeps the first n questions of each conversation.
	MaxQuestions int
}

// Apply returns the conversations f keeps. The input is not modified.
func (f Filter) Apply(convs []Conversation) []Conversation {
	keep := map[string]bool{}
	for _, c := range f.Categories {
		keep[c] = true
	}
	var out []Conversation
	for _, c := range convs {
		if f.MaxConversations > 0 && len(out) == f.MaxConversations {
			break
		}
		var qs []Question
		for _, q := range c.Questions {
			if len(keep) > 0 && !keep[q.Category] {
				continue
			}
			if f.MaxQuestions > 0 && len(qs) == f.MaxQuestions {
				break
			}
			qs = append(qs, q)
		}
		if len(qs) == 0 {
			continue
		}
		c.Questions = qs
		out = append(out, c)
	}
	return out
}
