package text

import (
	"regexp"
	"strings"
)

// ConvertMarkdownToMrkdwn converts standard Markdown syntax to Slack's native
// mrkdwn format. This allows messages to be sent via MsgOptionText() (top-level
// text field) instead of Block Kit blocks, avoiding Slack's aggressive "Show more"
// collapse behavior on block-based messages.
func ConvertMarkdownToMrkdwn(md string) string {
	lines := strings.Split(md, "\n")
	var result []string

	inCodeBlock := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCodeBlock = !inCodeBlock
			result = append(result, line)
			continue
		}
		if inCodeBlock {
			result = append(result, line)
			continue
		}
		result = append(result, convertLine(line))
	}

	return strings.Join(result, "\n")
}

func convertLine(line string) string {
	trimmed := strings.TrimSpace(line)

	// Headings: # heading -> *heading*
	if heading := matchHeading(trimmed); heading != "" {
		return "*" + heading + "*"
	}

	// Unordered list markers: "- item" / "* item" -> "• item"
	// Preserves leading whitespace for nested list indentation.
	if loc := reUnorderedList.FindStringIndex(line); loc != nil {
		// Replace the matched prefix (whitespace + marker + space) with (whitespace + bullet + space)
		prefix := line[loc[0]:loc[1]]
		// The last two characters of prefix are always "<marker> ", replace with "• "
		converted := prefix[:len(prefix)-2] + "• " + convertInline(line[loc[1]:])
		return converted
	}

	return convertInline(line)
}

// matchHeading returns the heading text if the line is a markdown heading, empty otherwise.
func matchHeading(line string) string {
	if !strings.HasPrefix(line, "#") {
		return ""
	}
	// Strip leading # characters and space
	i := 0
	for i < len(line) && line[i] == '#' {
		i++
	}
	if i > 0 && i < len(line) && line[i] == ' ' {
		return strings.TrimSpace(line[i+1:])
	}
	return ""
}

var (
	// Unordered list markers: "- item" or "* item" at start of line (with optional leading whitespace)
	// Captures: (leading whitespace)(marker character)(rest of line including the space after marker)
	reUnorderedList = regexp.MustCompile(`^(\s*)[*\-] `)

	// Bold: **text** or __text__ -> *text*
	reBoldAsterisks   = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reBoldUnderscores = regexp.MustCompile(`__(.+?)__`)

	// Strikethrough: ~~text~~ -> ~text~
	reStrikethrough = regexp.MustCompile(`~~(.+?)~~`)

	// Links: [text](url) -> <url|text>
	reLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

	// Images: ![alt](url) -> <url|alt> (best effort)
	reImage = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
)

func convertInline(s string) string {
	// Images before links (images have ! prefix)
	s = reImage.ReplaceAllString(s, "<$2|$1>")

	// Links
	s = reLink.ReplaceAllString(s, "<$2|$1>")

	// Bold: **text** -> *text* (must come before italic handling)
	s = reBoldAsterisks.ReplaceAllString(s, "*$1*")
	s = reBoldUnderscores.ReplaceAllString(s, "*$1*")

	// Strikethrough: ~~text~~ -> ~text~
	s = reStrikethrough.ReplaceAllString(s, "~$1~")

	// Note: *italic* and `code` are already compatible between Markdown and Slack mrkdwn.
	// Blockquotes (> text) are also compatible.
	// Bullet list markers (- item, * item) are converted to • in convertLine().

	return s
}
