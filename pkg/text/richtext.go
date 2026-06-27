package text

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/slack-go/slack"
)

// ConvertMarkdownToRichTextBlock parses Markdown and produces a single rich_text
// block suitable for Slack's Block Kit. Rich text blocks render native lists,
// code blocks, and quotes without triggering Slack's "Show more" collapse.
func ConvertMarkdownToRichTextBlock(md string) *slack.RichTextBlock {
	lines := strings.Split(md, "\n")
	p := &rtParser{
		lines: lines,
	}
	p.parse()

	if len(p.elements) == 0 {
		return slack.NewRichTextBlock("", slack.NewRichTextSection(
			slack.NewRichTextSectionTextElement("", nil),
		))
	}

	return slack.NewRichTextBlock("", p.elements...)
}

// StripMarkdownForPlainText removes Markdown formatting to produce a plain text
// string suitable for the notification fallback text field. Slack mention tokens
// (<@U123>, <#C123>, <!here>, ...) are intentionally left intact: Slack resolves
// them server-side in the top-level message text field.
func StripMarkdownForPlainText(md string) string {
	lines := strings.Split(md, "\n")
	var result []string

	inCodeBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock {
			result = append(result, line)
			continue
		}

		// Strip heading markers
		if heading := matchHeading(trimmed); heading != "" {
			result = append(result, heading)
			continue
		}

		// Strip blockquote marker
		if strings.HasPrefix(trimmed, "> ") {
			result = append(result, trimmed[2:])
			continue
		}

		result = append(result, stripInlineMarkdown(line))
	}
	return strings.Join(result, "\n")
}

// stripInlineMarkdown removes inline Markdown formatting characters.
func stripInlineMarkdown(s string) string {
	// Links: [text](url) -> text
	s = reLink.ReplaceAllString(s, "$1")
	// Images: ![alt](url) -> alt
	s = reImage.ReplaceAllString(s, "$1")
	// Bold
	s = reBoldAsterisks.ReplaceAllString(s, "$1")
	s = reBoldUnderscores.ReplaceAllString(s, "$1")
	// Strikethrough
	s = reStrikethrough.ReplaceAllString(s, "$1")
	// Inline code: `code` -> code
	s = reInlineCode.ReplaceAllString(s, "$1")
	return s
}

var reInlineCode = regexp.MustCompile("`([^`]+)`")

// rtParser is a line-by-line state machine that converts Markdown lines into
// rich_text block elements.
type rtParser struct {
	lines    []string
	elements []RichTextElement

	// accumulators
	paragraphLines []string
	listItems      []listItem
	listStyle      slack.RichTextListElementType
	listIndent     int
	codeLines      []string
	inCodeBlock    bool
}

// RichTextElement is slack.RichTextElement -- we re-export nothing, just use
// the interface directly in our accumulator. The type alias here is only for
// the struct field; the public API uses slack types.
type RichTextElement = slack.RichTextElement

type listItem struct {
	text   string
	indent int
}

func (p *rtParser) parse() {
	for _, line := range p.lines {
		trimmed := strings.TrimSpace(line)

		// Code block fences
		if strings.HasPrefix(trimmed, "```") {
			if p.inCodeBlock {
				// Close code block
				p.inCodeBlock = false
				p.flushCodeBlock()
			} else {
				// Open code block -- flush any pending state first
				p.flushParagraph()
				p.flushList()
				p.inCodeBlock = true
			}
			continue
		}

		if p.inCodeBlock {
			p.codeLines = append(p.codeLines, line)
			continue
		}

		// Blank line -- flush accumulators
		if trimmed == "" {
			p.flushParagraph()
			p.flushList()
			continue
		}

		// Blockquote
		if strings.HasPrefix(trimmed, "> ") {
			p.flushParagraph()
			p.flushList()
			quoteText := trimmed[2:]
			p.emitQuote(quoteText)
			continue
		}

		// Heading
		if heading := matchHeading(trimmed); heading != "" {
			p.flushParagraph()
			p.flushList()
			p.emitHeading(heading)
			continue
		}

		// Unordered list
		if indent, itemText, ok := matchUnorderedListItem(line); ok {
			p.flushParagraph()
			p.addListItem(itemText, indent, slack.RTEListBullet)
			continue
		}

		// Ordered list
		if indent, itemText, ok := matchOrderedListItem(line); ok {
			p.flushParagraph()
			p.addListItem(itemText, indent, slack.RTEListOrdered)
			continue
		}

		// Regular text -- accumulate as paragraph
		p.flushList()
		p.paragraphLines = append(p.paragraphLines, line)
	}

	// Flush any remaining state
	if p.inCodeBlock {
		// Unclosed code block -- emit what we have
		p.flushCodeBlock()
	}
	p.flushParagraph()
	p.flushList()
}

func (p *rtParser) flushParagraph() {
	if len(p.paragraphLines) == 0 {
		return
	}
	text := strings.Join(p.paragraphLines, "\n")
	elems := parseInlineElements(text)
	// Append a trailing newline so paragraphs have spacing
	elems = append(elems, slack.NewRichTextSectionTextElement("\n", nil))
	p.elements = append(p.elements, slack.NewRichTextSection(elems...))
	p.paragraphLines = nil
}

func (p *rtParser) flushList() {
	if len(p.listItems) == 0 {
		return
	}

	// Group consecutive items by indent level. When indent changes, emit the
	// accumulated group and start a new one.
	type listGroup struct {
		indent int
		items  []listItem
	}
	var groups []listGroup
	for _, item := range p.listItems {
		if len(groups) == 0 || groups[len(groups)-1].indent != item.indent {
			groups = append(groups, listGroup{indent: item.indent, items: []listItem{item}})
		} else {
			groups[len(groups)-1].items = append(groups[len(groups)-1].items, item)
		}
	}

	for _, g := range groups {
		sections := make([]slack.RichTextElement, 0, len(g.items))
		for _, item := range g.items {
			elems := parseInlineElements(item.text)
			sections = append(sections, slack.NewRichTextSection(elems...))
		}
		p.elements = append(p.elements, slack.NewRichTextList(p.listStyle, g.indent, sections...))
	}

	p.listItems = nil
}

func (p *rtParser) addListItem(text string, indent int, style slack.RichTextListElementType) {
	// If the style changes (e.g., switching from bullet to ordered), flush the
	// current list first.
	if len(p.listItems) > 0 && p.listStyle != style {
		p.flushList()
	}
	p.listStyle = style
	p.listItems = append(p.listItems, listItem{text: text, indent: indent})
}

func (p *rtParser) flushCodeBlock() {
	code := strings.Join(p.codeLines, "\n")
	textElem := slack.NewRichTextSectionTextElement(code, nil)
	preformatted := &slack.RichTextPreformatted{
		RichTextSection: slack.RichTextSection{
			Type:     slack.RTEPreformatted,
			Elements: []slack.RichTextSectionElement{textElem},
		},
	}
	p.elements = append(p.elements, preformatted)
	p.codeLines = nil
}

func (p *rtParser) emitQuote(text string) {
	elems := parseInlineElements(text)
	quote := &slack.RichTextQuote{
		Type:     slack.RTEQuote,
		Elements: elems,
	}
	p.elements = append(p.elements, quote)
}

func (p *rtParser) emitHeading(text string) {
	elems := parseInlineElements(text)
	// Apply bold to all text elements since Slack has no heading concept
	boldElems := make([]slack.RichTextSectionElement, 0, len(elems))
	for _, elem := range elems {
		switch e := elem.(type) {
		case *slack.RichTextSectionTextElement:
			style := mergeStyle(e.Style, &slack.RichTextSectionTextStyle{Bold: true})
			boldElems = append(boldElems, slack.NewRichTextSectionTextElement(e.Text, style))
		case *slack.RichTextSectionLinkElement:
			style := mergeStyle(e.Style, &slack.RichTextSectionTextStyle{Bold: true})
			boldElems = append(boldElems, slack.NewRichTextSectionLinkElement(e.URL, e.Text, style))
		default:
			boldElems = append(boldElems, elem)
		}
	}
	boldElems = append(boldElems, slack.NewRichTextSectionTextElement("\n", nil))
	p.elements = append(p.elements, slack.NewRichTextSection(boldElems...))
}

// mergeStyle merges additional style flags into an existing style, creating a
// new style if the base is nil.
func mergeStyle(base, additional *slack.RichTextSectionTextStyle) *slack.RichTextSectionTextStyle {
	if base == nil {
		return additional
	}
	merged := *base
	if additional.Bold {
		merged.Bold = true
	}
	if additional.Italic {
		merged.Italic = true
	}
	if additional.Strike {
		merged.Strike = true
	}
	if additional.Code {
		merged.Code = true
	}
	return &merged
}

// --- Line matchers ---

var (
	reUnorderedListItem = regexp.MustCompile(`^(\s*)[*\-] (.*)$`)
	reOrderedListItem   = regexp.MustCompile(`^(\s*)\d+\. (.*)$`)
)

// matchUnorderedListItem returns (indent, text, true) if the line is an
// unordered list item.
func matchUnorderedListItem(line string) (int, string, bool) {
	m := reUnorderedListItem.FindStringSubmatch(line)
	if m == nil {
		return 0, "", false
	}
	indent := len(m[1]) / 2
	return indent, m[2], true
}

// matchOrderedListItem returns (indent, text, true) if the line is an ordered
// list item.
func matchOrderedListItem(line string) (int, string, bool) {
	m := reOrderedListItem.FindStringSubmatch(line)
	if m == nil {
		return 0, "", false
	}
	indent := len(m[1]) / 2
	return indent, m[2], true
}

// --- Inline element parser ---

// parseInlineElements parses inline Markdown formatting in text and returns a
// slice of RichTextSectionElement values. It handles bold, italic,
// strikethrough, inline code, and links, including nested styles.
func parseInlineElements(text string) []slack.RichTextSectionElement {
	if text == "" {
		return []slack.RichTextSectionElement{
			slack.NewRichTextSectionTextElement("", nil),
		}
	}
	return parseInline(text, nil)
}

// parseInline recursively parses inline formatting, applying the given base
// style to all emitted text elements. This handles nesting: when we find bold
// inside a region that's already italic, the resulting style has both flags.
func parseInline(text string, baseStyle *slack.RichTextSectionTextStyle) []slack.RichTextSectionElement {
	if text == "" {
		return nil
	}

	var elements []slack.RichTextSectionElement
	i := 0
	plainStart := 0

	flushPlain := func(end int) {
		if end > plainStart {
			elements = append(elements, slack.NewRichTextSectionTextElement(
				text[plainStart:end], copyStyle(baseStyle),
			))
		}
	}

	for i < len(text) {
		// Link: [text](url)
		if text[i] == '[' {
			if linkText, url, end, ok := matchLinkAt(text, i); ok {
				flushPlain(i)
				elements = append(elements, slack.NewRichTextSectionLinkElement(
					url, linkText, copyStyle(baseStyle),
				))
				i = end
				plainStart = i
				continue
			}
		}

		// Inline code: `code`
		if text[i] == '`' {
			if content, end, ok := matchDelimitedAt(text, i, "`", "`"); ok {
				flushPlain(i)
				codeStyle := mergeStyle(baseStyle, &slack.RichTextSectionTextStyle{Code: true})
				elements = append(elements, slack.NewRichTextSectionTextElement(content, codeStyle))
				i = end
				plainStart = i
				continue
			}
		}

		// Bold: **text** or __text__
		if i+1 < len(text) {
			if (text[i] == '*' && text[i+1] == '*') || (text[i] == '_' && text[i+1] == '_') {
				delim := text[i : i+2]
				if content, end, ok := matchDelimitedAt(text, i, delim, delim); ok {
					// Underscore emphasis must respect intraword flanking; asterisks do not.
					if text[i] != '_' || underscoreEmphasisAllowed(text, i, end) {
						flushPlain(i)
						boldStyle := mergeStyle(baseStyle, &slack.RichTextSectionTextStyle{Bold: true})
						inner := parseInline(content, boldStyle)
						elements = append(elements, inner...)
						i = end
						plainStart = i
						continue
					}
				}
			}
		}

		// Strikethrough: ~~text~~
		if i+1 < len(text) && text[i] == '~' && text[i+1] == '~' {
			if content, end, ok := matchDelimitedAt(text, i, "~~", "~~"); ok {
				flushPlain(i)
				strikeStyle := mergeStyle(baseStyle, &slack.RichTextSectionTextStyle{Strike: true})
				inner := parseInline(content, strikeStyle)
				elements = append(elements, inner...)
				i = end
				plainStart = i
				continue
			}
		}

		// Italic: *text* or _text_
		// Must come after bold check to avoid consuming ** as two single *
		if text[i] == '*' || text[i] == '_' {
			delim := string(text[i])
			// Make sure this is not a double delimiter (handled above)
			if i+1 < len(text) && text[i+1] != text[i] {
				if content, end, ok := matchDelimitedAt(text, i, delim, delim); ok {
					// Underscore emphasis must respect intraword flanking; asterisks do not.
					if text[i] != '_' || underscoreEmphasisAllowed(text, i, end) {
						flushPlain(i)
						italicStyle := mergeStyle(baseStyle, &slack.RichTextSectionTextStyle{Italic: true})
						inner := parseInline(content, italicStyle)
						elements = append(elements, inner...)
						i = end
						plainStart = i
						continue
					}
				}
			}
		}

		// Slack mention: <@U123>, <#C123|name>, <!subteam^S123>, <!here>, <!channel>
		if text[i] == '<' {
			if elem, end, ok := parseSlackMention(text, i, baseStyle); ok {
				flushPlain(i)
				elements = append(elements, elem)
				i = end
				plainStart = i
				continue
			}
		}

		i++
	}

	flushPlain(len(text))
	return elements
}

// matchDelimitedAt tries to match text enclosed by open and close delimiters
// starting at position pos. Returns the content, the position after the closing
// delimiter, and whether a match was found.
func matchDelimitedAt(text string, pos int, open, close string) (string, int, bool) {
	if !strings.HasPrefix(text[pos:], open) {
		return "", 0, false
	}
	start := pos + len(open)
	// Don't match empty content
	if start >= len(text) {
		return "", 0, false
	}
	end := strings.Index(text[start:], close)
	if end < 0 || end == 0 {
		return "", 0, false
	}
	content := text[start : start+end]
	return content, start + end + len(close), true
}

// matchLinkAt tries to match [text](url) starting at position pos. Returns the
// link text, URL, the position after the closing paren, and whether a match was
// found.
func matchLinkAt(text string, pos int) (string, string, int, bool) {
	if text[pos] != '[' {
		return "", "", 0, false
	}
	// Find closing bracket
	closeBracket := strings.Index(text[pos:], "](")
	if closeBracket < 0 {
		return "", "", 0, false
	}
	closeBracket += pos
	linkText := text[pos+1 : closeBracket]
	if linkText == "" {
		return "", "", 0, false
	}
	// Find closing paren
	urlStart := closeBracket + 2
	closeParen := strings.Index(text[urlStart:], ")")
	if closeParen < 0 {
		return "", "", 0, false
	}
	url := text[urlStart : urlStart+closeParen]
	if url == "" {
		return "", "", 0, false
	}
	return linkText, url, urlStart + closeParen + 1, true
}

// copyStyle returns a shallow copy of the style, or nil if the input is nil.
func copyStyle(s *slack.RichTextSectionTextStyle) *slack.RichTextSectionTextStyle {
	if s == nil {
		return nil
	}
	cpy := *s
	return &cpy
}

// isWordChar reports whether r is a "word" character for CommonMark-style
// intraword underscore detection: an underscore, or any Unicode letter or digit.
func isWordChar(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// underscoreEmphasisAllowed applies a simplified CommonMark flanking rule for
// `_`/`__` emphasis. The opening delimiter starts at index i; end is the index
// just past the closing delimiter. Emphasis is allowed only when the opening
// delimiter is at string start (or preceded by a non-word rune) AND the closing
// delimiter is at string end (or followed by a non-word rune). This blocks
// intraword underscores (foo_bar_baz, snake_case_name, foo__bar__baz) while
// preserving boundary emphasis (_italic_, __bold__). Treating `_` itself as a
// word char keeps the second `_` of a `__` run from re-opening as a single-
// underscore italic on the fall-through pass.
func underscoreEmphasisAllowed(text string, i, end int) bool {
	if i > 0 {
		before, _ := utf8.DecodeLastRuneInString(text[:i])
		if isWordChar(before) {
			return false
		}
	}
	if end < len(text) {
		after, _ := utf8.DecodeRuneInString(text[end:])
		if isWordChar(after) {
			return false
		}
	}
	return true
}

// isValidMentionID reports whether id is a plausible Slack object ID: a non-empty
// run of ASCII [A-Z0-9] whose first byte is one of allowedPrefixes.
func isValidMentionID(id, allowedPrefixes string) bool {
	if id == "" || strings.IndexByte(allowedPrefixes, id[0]) < 0 {
		return false
	}
	for j := 0; j < len(id); j++ {
		c := id[j]
		if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// parseSlackMention parses a Slack mention token beginning at text[i] (which must
// be '<'): <@U123>, <@U123|label>, <#C123|label>, <!subteam^S123|label>, <!here>,
// <!channel>, or <!everyone>. On success it returns the typed rich_text element,
// the index just past the closing '>', and true. For any malformed or
// unrecognized token it returns (nil, 0, false) so the caller treats '<' as a
// literal character. user/channel elements carry baseStyle; usergroup/broadcast
// elements have no Style field in slack-go v0.19.0 and are emitted unstyled.
func parseSlackMention(text string, i int, baseStyle *slack.RichTextSectionTextStyle) (slack.RichTextSectionElement, int, bool) {
	if i >= len(text) || text[i] != '<' {
		return nil, 0, false
	}
	rel := strings.IndexByte(text[i:], '>')
	if rel < 0 {
		return nil, 0, false
	}
	closeIdx := i + rel
	inner := text[i+1 : closeIdx]
	newIndex := closeIdx + 1

	// Strip the optional "|label" fallback Slack permits.
	id := inner
	if pipe := strings.IndexByte(inner, '|'); pipe >= 0 {
		id = inner[:pipe]
	}
	if id == "" {
		return nil, 0, false
	}

	switch id[0] {
	case '@': // user mention
		userID := id[1:]
		if !isValidMentionID(userID, "UWB") {
			return nil, 0, false
		}
		return slack.NewRichTextSectionUserElement(userID, copyStyle(baseStyle)), newIndex, true
	case '#': // channel mention
		channelID := id[1:]
		if !isValidMentionID(channelID, "CGD") {
			return nil, 0, false
		}
		return slack.NewRichTextSectionChannelElement(channelID, copyStyle(baseStyle)), newIndex, true
	case '!': // broadcast or usergroup
		switch {
		case id == "!here", id == "!channel", id == "!everyone":
			return slack.NewRichTextSectionBroadcastElement(id[1:]), newIndex, true
		case strings.HasPrefix(id, "!subteam^"):
			groupID := id[len("!subteam^"):]
			if !isValidMentionID(groupID, "S") {
				return nil, 0, false
			}
			return slack.NewRichTextSectionUserGroupElement(groupID), newIndex, true
		}
	}
	return nil, 0, false
}
