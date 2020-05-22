package ui

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/charmbracelet/boba"
	"github.com/charmbracelet/boba/textinput"
	"github.com/charmbracelet/boba/viewport"
	"github.com/charmbracelet/charm"
	"github.com/charmbracelet/charm/ui/common"
	"github.com/charmbracelet/glamour"
	te "github.com/muesli/termenv"
)

const (
	maxDocumentWidth = 120
	statusBarHeight  = 1
	gray             = "#333333"
	yellowGreen      = "#ECFD65"
	fuschia          = "#EE6FF8"
	noteHeadingText  = " Set Memo "
	notePromptText   = " > "
)

var (
	noteHeading = te.String(noteHeadingText).
			Foreground(common.Cream.Color()).
			Background(common.Green.Color()).
			String()

	statusBarBg          = common.NewColorPair("#242424", "#E6E6E6")
	statusBarNoteFg      = common.NewColorPair("#7D7D7D", "#656565")
	statusBarScrollPosFg = common.NewColorPair("#5A5A5A", "#949494")
)

// MSG

type contentRenderedMsg string
type noteSavedMsg *charm.Markdown

// MODEL

type pagerState int

const (
	pagerStateBrowse pagerState = iota
	pagerStateSetNote
)

type pagerModel struct {
	cc           *charm.Client
	viewport     viewport.Model
	state        pagerState
	glamourStyle string
	width        int
	height       int
	textInput    textinput.Model

	// Current document being rendered, sans-glamour rendering. We cache
	// this here so we can re-render it on resize.
	currentDocument *markdown
}

func newPagerModel(glamourStyle string) pagerModel {
	ti := textinput.NewModel()
	ti.Prompt = te.String(notePromptText).
		Foreground(te.ColorProfile().Color(gray)).
		Background(te.ColorProfile().Color(yellowGreen)).
		String()
	ti.TextColor = gray
	ti.BackgroundColor = yellowGreen
	ti.CursorColor = fuschia
	ti.CharLimit = noteCharacterLimit
	ti.Focus()

	return pagerModel{
		state:        pagerStateBrowse,
		glamourStyle: glamourStyle,
		textInput:    ti,
	}
}

func (m *pagerModel) setSize(w, h int) {
	m.width = w
	m.height = h
	m.viewport.Width = w
	m.viewport.Height = h - statusBarHeight
	m.textInput.Width = w - len(noteHeadingText) - len(notePromptText) - 1
}

func (m *pagerModel) setContent(s string) {
	m.viewport.SetContent(s)
}

func (m *pagerModel) unload() {
	m.state = pagerStateBrowse
	m.viewport.SetContent("")
	m.viewport.Y = 0
	m.textInput.Reset()
}

// UPDATE

func pagerUpdate(msg boba.Msg, m pagerModel) (pagerModel, boba.Cmd) {
	var (
		cmd  boba.Cmd
		cmds []boba.Cmd
	)

	switch msg := msg.(type) {

	case boba.KeyMsg:
		switch m.state {
		case pagerStateSetNote:
			switch msg.String() {
			case "q":
				fallthrough
			case "esc":
				m.state = pagerStateBrowse
				return m, nil
			case "enter":
				var cmd boba.Cmd
				if m.textInput.Value() != m.currentDocument.Note { // don't update if the note didn't change
					m.currentDocument.Note = m.textInput.Value() // update optimistically
					cmd = saveDocumentNote(m.cc, m.currentDocument.ID, m.currentDocument.Note)
				}
				m.state = pagerStateBrowse
				m.textInput.Reset()
				return m, cmd
			}
		default:
			switch msg.String() {
			case "q":
				fallthrough
			case "esc":
				if m.state != pagerStateBrowse {
					m.state = pagerStateBrowse
					return m, nil
				}
			case "n":
				// Users can't set the note on news markdown
				if m.currentDocument.markdownType == newsMarkdown {
					break
				}

				m.state = pagerStateSetNote
				if m.textInput.Value() == "" {
					// Pre-populate with existing value
					m.textInput.SetValue(m.currentDocument.Note)
					m.textInput.CursorEnd()
				}
				return m, textinput.Blink(m.textInput)
			}
		}

	// Glow has rendered the content
	case contentRenderedMsg:
		m.setContent(string(msg))
		return m, nil

	// We've reveived terminal dimensions, either for the first time or
	// after a resize
	case terminalSizeMsg:
		if msg.Error() != nil {
			// This will be caught at the top level
			return m, nil
		}

		var cmd boba.Cmd
		if m.currentDocument != nil {
			cmd = renderWithGlamour(m, m.currentDocument.Body)
		}
		return m, cmd
	}

	switch m.state {
	case pagerStateSetNote:
		m.textInput, cmd = textinput.Update(msg, m.textInput)
		cmds = append(cmds, cmd)
	default:
		m.viewport, cmd = viewport.Update(msg, m.viewport)
		cmds = append(cmds, cmd)
	}

	return m, boba.Batch(cmds...)
}

// VIEW

func pagerView(m pagerModel) string {
	var footer string
	if m.state == pagerStateSetNote {
		footer = pagerSetNoteView(m)
	} else {
		footer = pagerStatusBarView(m)
	}

	return fmt.Sprintf(
		"\n%s\n%s",
		viewport.View(m.viewport),
		footer,
	)
}

func pagerStatusBarView(m pagerModel) string {
	// Logo
	logoText := " Glow "
	logo := glowLogoView(logoText)

	// Scroll percent
	scrollPercent := math.Max(0.0, math.Min(1.0, m.viewport.ScrollPercent()))
	percentText := fmt.Sprintf(" %3.f%% ", scrollPercent*100)
	percent := te.String(percentText).
		Foreground(statusBarScrollPosFg.Color()).
		Background(statusBarBg.Color()).
		String()

	// Note
	noteText := m.currentDocument.Note
	if len(noteText) == 0 {
		noteText = "(No title)"
	}
	noteText = truncate(" "+noteText+" ", max(0, m.width-len(logoText)-len(percentText)))
	note := te.String(noteText).
		Foreground(statusBarNoteFg.Color()).
		Background(statusBarBg.Color()).String()

	// Empty space
	emptyCell := te.String(" ").Background(statusBarBg.Color()).String()
	padding := max(0, m.width-len(logoText)-len(noteText)-len(percentText))
	emptySpace := strings.Repeat(emptyCell, padding)

	return logo + note + emptySpace + percent
}

func pagerSetNoteView(m pagerModel) string {
	return noteHeading + textinput.View(m.textInput)
}

// CMD

func renderWithGlamour(m pagerModel, md string) boba.Cmd {
	return func() boba.Msg {
		s, err := glamourRender(m, md)
		if err != nil {
			return errMsg(err)
		}
		return contentRenderedMsg(s)
	}
}

func saveDocumentNote(cc *charm.Client, id int, note string) boba.Cmd {
	if cc == nil {
		return func() boba.Msg {
			return errMsg(errors.New("can't set note; no charm client"))
		}
	}
	return func() boba.Msg {
		if err := cc.SetMarkdownNote(id, note); err != nil {
			return errMsg(err)
		}
		return noteSavedMsg(&charm.Markdown{ID: id, Note: note})
	}
}

// This is where the magic happens
func glamourRender(m pagerModel, markdown string) (string, error) {

	if os.Getenv("GLOW_DISABLE_GLAMOUR") != "" {
		return markdown, nil
	}

	// initialize glamour
	var gs glamour.TermRendererOption
	if m.glamourStyle == "auto" {
		gs = glamour.WithAutoStyle()
	} else {
		gs = glamour.WithStylePath(m.glamourStyle)
	}

	width := max(0, min(maxDocumentWidth, m.viewport.Width))
	r, err := glamour.NewTermRenderer(
		gs,
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", err
	}

	out, err := r.Render(markdown)
	if err != nil {
		return "", err
	}

	// trim lines
	lines := strings.Split(string(out), "\n")

	var content string
	for i, s := range lines {
		content += strings.TrimSpace(s)

		// don't add an artificial newline after the last split
		if i+1 < len(lines) {
			content += "\n"
		}
	}

	return content, nil
}
