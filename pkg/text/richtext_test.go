package text

import (
	"testing"

	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertMarkdownToRichTextBlock_PlainText(t *testing.T) {
	block := ConvertMarkdownToRichTextBlock("Hello world")
	require.NotNil(t, block)
	assert.Equal(t, slack.MBTRichText, block.Type)
	require.Len(t, block.Elements, 1)

	section, ok := block.Elements[0].(*slack.RichTextSection)
	require.True(t, ok, "expected RichTextSection")
	// paragraph text + trailing newline
	require.Len(t, section.Elements, 2)

	textElem, ok := section.Elements[0].(*slack.RichTextSectionTextElement)
	require.True(t, ok)
	assert.Equal(t, "Hello world", textElem.Text)
	assert.Nil(t, textElem.Style)
}

func TestConvertMarkdownToRichTextBlock_BoldText(t *testing.T) {
	block := ConvertMarkdownToRichTextBlock("This is **bold** text")
	require.NotNil(t, block)
	require.Len(t, block.Elements, 1)

	section := block.Elements[0].(*slack.RichTextSection)
	// "This is " + bold "bold" + " text" + "\n"
	require.Len(t, section.Elements, 4)

	assertTextElement(t, section.Elements[0], "This is ", nil)
	assertTextElement(t, section.Elements[1], "bold", &slack.RichTextSectionTextStyle{Bold: true})
	assertTextElement(t, section.Elements[2], " text", nil)
}

func TestConvertMarkdownToRichTextBlock_ItalicText(t *testing.T) {
	block := ConvertMarkdownToRichTextBlock("This is *italic* text")
	require.NotNil(t, block)
	section := block.Elements[0].(*slack.RichTextSection)

	assertTextElement(t, section.Elements[0], "This is ", nil)
	assertTextElement(t, section.Elements[1], "italic", &slack.RichTextSectionTextStyle{Italic: true})
	assertTextElement(t, section.Elements[2], " text", nil)
}

func TestConvertMarkdownToRichTextBlock_StrikethroughText(t *testing.T) {
	block := ConvertMarkdownToRichTextBlock("This is ~~deleted~~ text")
	require.NotNil(t, block)
	section := block.Elements[0].(*slack.RichTextSection)

	assertTextElement(t, section.Elements[0], "This is ", nil)
	assertTextElement(t, section.Elements[1], "deleted", &slack.RichTextSectionTextStyle{Strike: true})
	assertTextElement(t, section.Elements[2], " text", nil)
}

func TestConvertMarkdownToRichTextBlock_InlineCode(t *testing.T) {
	block := ConvertMarkdownToRichTextBlock("Run `kubectl get pods`")
	require.NotNil(t, block)
	section := block.Elements[0].(*slack.RichTextSection)

	assertTextElement(t, section.Elements[0], "Run ", nil)
	assertTextElement(t, section.Elements[1], "kubectl get pods", &slack.RichTextSectionTextStyle{Code: true})
}

func TestConvertMarkdownToRichTextBlock_Link(t *testing.T) {
	block := ConvertMarkdownToRichTextBlock("Check [this link](https://example.com) out")
	require.NotNil(t, block)
	section := block.Elements[0].(*slack.RichTextSection)

	assertTextElement(t, section.Elements[0], "Check ", nil)

	linkElem, ok := section.Elements[1].(*slack.RichTextSectionLinkElement)
	require.True(t, ok, "expected RichTextSectionLinkElement, got %T", section.Elements[1])
	assert.Equal(t, "https://example.com", linkElem.URL)
	assert.Equal(t, "this link", linkElem.Text)

	assertTextElement(t, section.Elements[2], " out", nil)
}

func TestConvertMarkdownToRichTextBlock_BulletList(t *testing.T) {
	block := ConvertMarkdownToRichTextBlock("- Item one\n- Item two\n- Item three")
	require.NotNil(t, block)
	require.Len(t, block.Elements, 1)

	list, ok := block.Elements[0].(*slack.RichTextList)
	require.True(t, ok, "expected RichTextList, got %T", block.Elements[0])
	assert.Equal(t, slack.RTEListBullet, list.Style)
	assert.Equal(t, 0, list.Indent)
	require.Len(t, list.Elements, 3)

	assertListItemText(t, list.Elements[0], "Item one")
	assertListItemText(t, list.Elements[1], "Item two")
	assertListItemText(t, list.Elements[2], "Item three")
}

func TestConvertMarkdownToRichTextBlock_OrderedList(t *testing.T) {
	block := ConvertMarkdownToRichTextBlock("1. First\n2. Second\n3. Third")
	require.NotNil(t, block)
	require.Len(t, block.Elements, 1)

	list, ok := block.Elements[0].(*slack.RichTextList)
	require.True(t, ok, "expected RichTextList")
	assert.Equal(t, slack.RTEListOrdered, list.Style)
	require.Len(t, list.Elements, 3)

	assertListItemText(t, list.Elements[0], "First")
	assertListItemText(t, list.Elements[1], "Second")
	assertListItemText(t, list.Elements[2], "Third")
}

func TestConvertMarkdownToRichTextBlock_NestedBulletList(t *testing.T) {
	input := "- Top level\n  - Sub item\n    - Deep item"
	block := ConvertMarkdownToRichTextBlock(input)
	require.NotNil(t, block)

	// Three groups with different indent levels produce three separate lists
	require.Len(t, block.Elements, 3)

	list0, ok := block.Elements[0].(*slack.RichTextList)
	require.True(t, ok)
	assert.Equal(t, 0, list0.Indent)
	assert.Equal(t, slack.RTEListBullet, list0.Style)
	assertListItemText(t, list0.Elements[0], "Top level")

	list1, ok := block.Elements[1].(*slack.RichTextList)
	require.True(t, ok)
	assert.Equal(t, 1, list1.Indent)
	assertListItemText(t, list1.Elements[0], "Sub item")

	list2, ok := block.Elements[2].(*slack.RichTextList)
	require.True(t, ok)
	assert.Equal(t, 2, list2.Indent)
	assertListItemText(t, list2.Elements[0], "Deep item")
}

func TestConvertMarkdownToRichTextBlock_CodeBlock(t *testing.T) {
	input := "```\nfunc main() {\n  fmt.Println(\"hello\")\n}\n```"
	block := ConvertMarkdownToRichTextBlock(input)
	require.NotNil(t, block)
	require.Len(t, block.Elements, 1)

	pre, ok := block.Elements[0].(*slack.RichTextPreformatted)
	require.True(t, ok, "expected RichTextPreformatted, got %T", block.Elements[0])
	require.Len(t, pre.Elements, 1)

	textElem, ok := pre.Elements[0].(*slack.RichTextSectionTextElement)
	require.True(t, ok)
	assert.Equal(t, "func main() {\n  fmt.Println(\"hello\")\n}", textElem.Text)
}

func TestConvertMarkdownToRichTextBlock_CodeBlockWithLanguage(t *testing.T) {
	input := "```python\nprint('hello')\n```"
	block := ConvertMarkdownToRichTextBlock(input)
	require.NotNil(t, block)
	require.Len(t, block.Elements, 1)

	pre, ok := block.Elements[0].(*slack.RichTextPreformatted)
	require.True(t, ok)
	textElem := pre.Elements[0].(*slack.RichTextSectionTextElement)
	assert.Equal(t, "print('hello')", textElem.Text)
}

func TestConvertMarkdownToRichTextBlock_Blockquote(t *testing.T) {
	block := ConvertMarkdownToRichTextBlock("> This is a quote")
	require.NotNil(t, block)
	require.Len(t, block.Elements, 1)

	quote, ok := block.Elements[0].(*slack.RichTextQuote)
	require.True(t, ok, "expected RichTextQuote, got %T", block.Elements[0])
	require.Len(t, quote.Elements, 1)

	textElem, ok := quote.Elements[0].(*slack.RichTextSectionTextElement)
	require.True(t, ok)
	assert.Equal(t, "This is a quote", textElem.Text)
}

func TestConvertMarkdownToRichTextBlock_Heading(t *testing.T) {
	block := ConvertMarkdownToRichTextBlock("# Root Cause")
	require.NotNil(t, block)
	require.Len(t, block.Elements, 1)

	section, ok := block.Elements[0].(*slack.RichTextSection)
	require.True(t, ok)
	// bold text + trailing newline
	require.Len(t, section.Elements, 2)

	textElem, ok := section.Elements[0].(*slack.RichTextSectionTextElement)
	require.True(t, ok)
	assert.Equal(t, "Root Cause", textElem.Text)
	require.NotNil(t, textElem.Style)
	assert.True(t, textElem.Style.Bold)
}

func TestConvertMarkdownToRichTextBlock_MixedContent(t *testing.T) {
	input := "# Summary\n\nThis is a paragraph.\n\n- Item A\n- Item B\n\n```\ncode here\n```\n\nFinal paragraph."
	block := ConvertMarkdownToRichTextBlock(input)
	require.NotNil(t, block)

	// Heading + paragraph + bullet list + code block + final paragraph
	require.Len(t, block.Elements, 5, "expected 5 elements for mixed content, got %d", len(block.Elements))

	// Heading
	_, ok := block.Elements[0].(*slack.RichTextSection)
	assert.True(t, ok, "element 0 should be RichTextSection (heading)")

	// Paragraph
	_, ok = block.Elements[1].(*slack.RichTextSection)
	assert.True(t, ok, "element 1 should be RichTextSection (paragraph)")

	// Bullet list
	list, ok := block.Elements[2].(*slack.RichTextList)
	assert.True(t, ok, "element 2 should be RichTextList")
	assert.Equal(t, slack.RTEListBullet, list.Style)
	require.Len(t, list.Elements, 2)

	// Code block
	_, ok = block.Elements[3].(*slack.RichTextPreformatted)
	assert.True(t, ok, "element 3 should be RichTextPreformatted")

	// Final paragraph
	_, ok = block.Elements[4].(*slack.RichTextSection)
	assert.True(t, ok, "element 4 should be RichTextSection (final paragraph)")
}

func TestConvertMarkdownToRichTextBlock_EmptyInput(t *testing.T) {
	block := ConvertMarkdownToRichTextBlock("")
	require.NotNil(t, block)
	assert.Equal(t, slack.MBTRichText, block.Type)
	// Should produce a minimal valid block
	require.Len(t, block.Elements, 1)
}

func TestConvertMarkdownToRichTextBlock_NestedInlineStyles(t *testing.T) {
	block := ConvertMarkdownToRichTextBlock("This is **bold and `code`** here")
	require.NotNil(t, block)
	section := block.Elements[0].(*slack.RichTextSection)

	// "This is " + bold "bold and " + bold+code "code" + " here" + "\n"
	require.Len(t, section.Elements, 5)

	assertTextElement(t, section.Elements[0], "This is ", nil)
	assertTextElement(t, section.Elements[1], "bold and ", &slack.RichTextSectionTextStyle{Bold: true})
	assertTextElement(t, section.Elements[2], "code", &slack.RichTextSectionTextStyle{Bold: true, Code: true})
	assertTextElement(t, section.Elements[3], " here", nil)
}

func TestConvertMarkdownToRichTextBlock_BoldUnderscores(t *testing.T) {
	block := ConvertMarkdownToRichTextBlock("Use __bold__ here")
	require.NotNil(t, block)
	section := block.Elements[0].(*slack.RichTextSection)

	assertTextElement(t, section.Elements[0], "Use ", nil)
	assertTextElement(t, section.Elements[1], "bold", &slack.RichTextSectionTextStyle{Bold: true})
	assertTextElement(t, section.Elements[2], " here", nil)
}

func TestConvertMarkdownToRichTextBlock_BulletListWithFormatting(t *testing.T) {
	block := ConvertMarkdownToRichTextBlock("- This has **bold** text\n- And `code` too")
	require.NotNil(t, block)

	list, ok := block.Elements[0].(*slack.RichTextList)
	require.True(t, ok)
	require.Len(t, list.Elements, 2)

	// First item: "This has " + bold "bold" + " text"
	section0 := list.Elements[0].(*slack.RichTextSection)
	require.Len(t, section0.Elements, 3)
	assertTextElement(t, section0.Elements[0], "This has ", nil)
	assertTextElement(t, section0.Elements[1], "bold", &slack.RichTextSectionTextStyle{Bold: true})
	assertTextElement(t, section0.Elements[2], " text", nil)
}

func TestConvertMarkdownToRichTextBlock_MultipleHeadingLevels(t *testing.T) {
	tests := []struct {
		name  string
		input string
		text  string
	}{
		{"h1", "# Title", "Title"},
		{"h2", "## Subtitle", "Subtitle"},
		{"h3", "### Section", "Section"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			block := ConvertMarkdownToRichTextBlock(tt.input)
			section := block.Elements[0].(*slack.RichTextSection)
			textElem := section.Elements[0].(*slack.RichTextSectionTextElement)
			assert.Equal(t, tt.text, textElem.Text)
			assert.True(t, textElem.Style.Bold)
		})
	}
}

func TestConvertMarkdownToRichTextBlock_AsterisksListVsItalic(t *testing.T) {
	// "* item" at start of line is a list, "*italic*" is formatting
	block := ConvertMarkdownToRichTextBlock("* list item")
	require.NotNil(t, block)
	list, ok := block.Elements[0].(*slack.RichTextList)
	require.True(t, ok, "expected list for '* item', got %T", block.Elements[0])
	assert.Equal(t, slack.RTEListBullet, list.Style)
}

func TestConvertMarkdownToRichTextBlock_BlockquoteWithFormatting(t *testing.T) {
	block := ConvertMarkdownToRichTextBlock("> This is **bold** in a quote")
	require.NotNil(t, block)

	quote, ok := block.Elements[0].(*slack.RichTextQuote)
	require.True(t, ok)
	require.Len(t, quote.Elements, 3)

	assertTextElement(t, quote.Elements[0], "This is ", nil)
	assertTextElement(t, quote.Elements[1], "bold", &slack.RichTextSectionTextStyle{Bold: true})
	assertTextElement(t, quote.Elements[2], " in a quote", nil)
}

func TestStripMarkdownForPlainText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "bold",
			input:    "This is **bold** text",
			expected: "This is bold text",
		},
		{
			name:     "link",
			input:    "Check [this](https://example.com)",
			expected: "Check this",
		},
		{
			name:     "heading",
			input:    "# Title",
			expected: "Title",
		},
		{
			name:     "code block stripped",
			input:    "```\nsome code\n```",
			expected: "some code",
		},
		{
			name:     "blockquote stripped",
			input:    "> A quote",
			expected: "A quote",
		},
		{
			name:     "plain text unchanged",
			input:    "No formatting here",
			expected: "No formatting here",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripMarkdownForPlainText(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// --- Test helpers ---

func assertTextElement(t *testing.T, elem slack.RichTextSectionElement, expectedText string, expectedStyle *slack.RichTextSectionTextStyle) {
	t.Helper()
	textElem, ok := elem.(*slack.RichTextSectionTextElement)
	if !ok {
		t.Fatalf("expected *RichTextSectionTextElement, got %T", elem)
	}
	assert.Equal(t, expectedText, textElem.Text)
	if expectedStyle == nil {
		if textElem.Style != nil {
			// Allow empty styles (all false) to match nil
			if textElem.Style.Bold || textElem.Style.Italic || textElem.Style.Strike || textElem.Style.Code {
				t.Errorf("expected nil style for text %q, got %+v", expectedText, textElem.Style)
			}
		}
	} else {
		require.NotNil(t, textElem.Style, "expected style %+v for text %q, got nil", expectedStyle, expectedText)
		assert.Equal(t, expectedStyle.Bold, textElem.Style.Bold, "Bold mismatch for %q", expectedText)
		assert.Equal(t, expectedStyle.Italic, textElem.Style.Italic, "Italic mismatch for %q", expectedText)
		assert.Equal(t, expectedStyle.Strike, textElem.Style.Strike, "Strike mismatch for %q", expectedText)
		assert.Equal(t, expectedStyle.Code, textElem.Style.Code, "Code mismatch for %q", expectedText)
	}
}

func assertListItemText(t *testing.T, elem slack.RichTextElement, expectedText string) {
	t.Helper()
	section, ok := elem.(*slack.RichTextSection)
	if !ok {
		t.Fatalf("expected *RichTextSection for list item, got %T", elem)
	}
	require.NotEmpty(t, section.Elements, "list item section has no elements")
	textElem, ok := section.Elements[0].(*slack.RichTextSectionTextElement)
	if !ok {
		t.Fatalf("expected *RichTextSectionTextElement in list item, got %T", section.Elements[0])
	}
	assert.Equal(t, expectedText, textElem.Text)
}
