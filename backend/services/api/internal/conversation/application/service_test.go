package application

import (
	"ai-bud/services/api/internal/conversation/domain"
	"strings"
	"testing"
)

func TestHistoryBoundedAndOnlyCompleted(t *testing.T) {
	turns := []domain.Turn{}
	for i := 0; i < 10; i++ {
		turns = append(turns, domain.Turn{State: "completed", Question: strings.Repeat("я", 4000), Answer: &domain.Answer{Steps: []domain.Step{{Text: strings.Repeat("ю", 8000)}}}})
	}
	turns = append(turns, domain.Turn{State: "failed", Question: "secret failed question"}, domain.Turn{State: "pending", Question: "pending"})
	messages := history(turns)
	size := 0
	for i, m := range messages {
		size += len([]rune(m.Content))
		expected := "user"
		if i%2 == 1 {
			expected = "assistant"
		}
		if m.Role != expected || strings.Contains(m.Content, "secret") {
			t.Fatal(messages)
		}
	}
	if len(messages) != 4 || size != 8000 {
		t.Fatal(len(messages), size)
	}
	short := []domain.Turn{}
	for i := 0; i < 10; i++ {
		short = append(short, domain.Turn{State: "completed", Question: "q", Answer: &domain.Answer{NextStep: "next"}})
	}
	if len(history(short)) != 6 {
		t.Fatal(history(short))
	}
	if len(history([]domain.Turn{{State: "completed", Question: "empty", Answer: &domain.Answer{}}})) != 0 {
		t.Fatal("empty answer included")
	}
}
