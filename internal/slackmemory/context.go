package slackmemory

import (
	"fmt"
	"strings"

	"github.com/yjwong/lark-cli/internal/platform"
)

const defaultMaxSectionChars = 2000

type ContextOptions struct {
	MaxSectionChars int
}

func BuildPromptContext(store *Store, event platform.MessageEvent, opts ContextOptions) (string, error) {
	if store == nil || !store.Enabled() {
		return "", nil
	}

	max := opts.MaxSectionChars
	if max <= 0 {
		max = defaultMaxSectionChars
	}

	sections := []struct {
		title string
		path  string
	}{
		{title: "Slack channel memory", path: store.ChannelMemoryPath(event)},
		{title: "Slack thread memory", path: store.ThreadMemoryPath(event)},
		{title: "Slack thread summary", path: store.ThreadSummaryPath(event)},
	}

	parts := make([]string, 0, len(sections))
	for _, section := range sections {
		text, err := store.ReadMarkdown(section.path, max)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", section.title, err)
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		parts = append(parts, "## "+section.title+"\n"+text)
	}

	return strings.Join(parts, "\n\n"), nil
}
