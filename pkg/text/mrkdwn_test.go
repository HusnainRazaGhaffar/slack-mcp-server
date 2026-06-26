package text

import (
	"testing"
)

func TestConvertMarkdownToMrkdwn(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "bold with asterisks",
			input:    "This is **bold** text",
			expected: "This is *bold* text",
		},
		{
			name:     "bold with underscores",
			input:    "This is __bold__ text",
			expected: "This is *bold* text",
		},
		{
			name:     "strikethrough",
			input:    "This is ~~deleted~~ text",
			expected: "This is ~deleted~ text",
		},
		{
			name:     "link",
			input:    "Check [this link](https://example.com)",
			expected: "Check <https://example.com|this link>",
		},
		{
			name:     "image",
			input:    "![alt text](https://example.com/img.png)",
			expected: "<https://example.com/img.png|alt text>",
		},
		{
			name:     "heading h1",
			input:    "# Root Cause",
			expected: "*Root Cause*",
		},
		{
			name:     "heading h2",
			input:    "## Current Status",
			expected: "*Current Status*",
		},
		{
			name:     "heading h3",
			input:    "### Details",
			expected: "*Details*",
		},
		{
			name:     "italic preserved",
			input:    "This is *italic* text",
			expected: "This is *italic* text",
		},
		{
			name:     "inline code preserved",
			input:    "Run `kubectl get pods`",
			expected: "Run `kubectl get pods`",
		},
		{
			name:     "blockquote preserved",
			input:    "> This is a quote",
			expected: "> This is a quote",
		},
		{
			name:     "bullet list dash converted",
			input:    "- Item one\n- Item two",
			expected: "• Item one\n• Item two",
		},
		{
			name:     "bullet list asterisk converted",
			input:    "* Item one\n* Item two",
			expected: "• Item one\n• Item two",
		},
		{
			name:     "nested bullet list preserves indentation",
			input:    "- Top level\n  - Sub item\n    - Deep item",
			expected: "• Top level\n  • Sub item\n    • Deep item",
		},
		{
			name:     "asterisk italic not converted to bullet",
			input:    "*italic text* here",
			expected: "*italic text* here",
		},
		{
			name:     "asterisk list vs italic distinction",
			input:    "* list item\n*italic* word",
			expected: "• list item\n*italic* word",
		},
		{
			name:     "numbered list unchanged",
			input:    "1. First\n2. Second\n3. Third",
			expected: "1. First\n2. Second\n3. Third",
		},
		{
			name:     "bullet with inline bold",
			input:    "- This has **bold** text",
			expected: "• This has *bold* text",
		},
		{
			name:     "code block preserved",
			input:    "```\nfunc main() {\n  fmt.Println(\"hello\")\n}\n```",
			expected: "```\nfunc main() {\n  fmt.Println(\"hello\")\n}\n```",
		},
		{
			name:     "bold inside code block not converted",
			input:    "```\n**not bold**\n```",
			expected: "```\n**not bold**\n```",
		},
		{
			name:     "mixed formatting",
			input:    "*Root cause:* The `aws-node` pod hit an **i/o timeout**",
			expected: "*Root cause:* The `aws-node` pod hit an *i/o timeout*",
		},
		{
			name:     "multiline message",
			input:    "Nothing changed.\n\n*Root cause:* Transient issue.\n\n*Status:* Resolved.",
			expected: "Nothing changed.\n\n*Root cause:* Transient issue.\n\n*Status:* Resolved.",
		},
		{
			name:     "plain text unchanged",
			input:    "No action needed.",
			expected: "No action needed.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertMarkdownToMrkdwn(tt.input)
			if got != tt.expected {
				t.Errorf("ConvertMarkdownToMrkdwn(%q)\n  got:  %q\n  want: %q", tt.input, got, tt.expected)
			}
		})
	}
}
