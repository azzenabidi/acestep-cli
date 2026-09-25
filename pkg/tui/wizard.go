// Package tui provides the interactive wizard and the generation progress
// view. Both are Bubbletea programs, and both degrade to plain terminal output
// when stdin is not a TTY.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	subtleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	focusedStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("205")).Padding(0, 1)
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	successStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
)

// fieldKind identifies one wizard step.
type fieldKind int

const (
	fieldCaption fieldKind = iota
	fieldStyle
	fieldLyrics
	fieldVocals
	fieldDuration
	fieldFormat
	fieldConfirm
)

type fieldSpec struct {
	kind  fieldKind
	label string
	hint  string
	// choices, when non-empty, makes the field a picker instead of an input.
	choices []string
}

var wizardFields = []fieldSpec{
	{kind: fieldCaption, label: "Describe the song", hint: "style, mood, instruments"},
	{kind: fieldStyle, label: "Add a style tag (optional)", hint: "e.g. lofi, cinematic, 90s"},
	{kind: fieldLyrics, label: "Lyrics (optional)", hint: "leave empty to let the model write them"},
	{kind: fieldVocals, label: "Vocals", hint: ""},
	{kind: fieldDuration, label: "Duration in seconds", hint: "0 lets the model decide"},
	{kind: fieldFormat, label: "Output format", hint: ""},
	{kind: fieldConfirm, label: "Generate?", hint: ""},
}

// Answer is the completed wizard.
type Answer struct {
	Caption      string
	Style        string
	Lyrics       string
	Instrumental bool
	Duration     float64
	Format       string
}

// wizardModel is the Bubbletea state machine.
type wizardModel struct {
	fields  []fieldSpec
	current int

	inputs     []textinput.Model
	vocalIndex int
	formatIdx  int
	choice     int

	err    error
	cancel bool
	answer Answer
	done   bool
}

func newWizard() *wizardModel {
	inputs := make([]textinput.Model, 0, len(wizardFields))
	for _, f := range wizardFields {
		ti := textinput.New()
		ti.Placeholder = f.hint
		ti.CharLimit = 2000
		switch f.kind {
		case fieldDuration:
			ti.SetValue("60")
		case fieldCaption:
			ti.Focus()
		}
		inputs = append(inputs, ti)
	}
	return &wizardModel{
		fields:     wizardFields,
		inputs:     inputs,
		vocalIndex: 0,
		formatIdx:  0,
	}
}

func (m *wizardModel) Init() tea.Cmd { return nil }

func (m *wizardModel) isChoice(k fieldKind) bool {
	return k == fieldVocals || k == fieldFormat
}

func (m *wizardModel) choicesFor(k fieldKind) []string {
	switch k {
	case fieldVocals:
		return []string{"with vocals", "instrumental"}
	case fieldFormat:
		return []string{"mp3", "wav16", "wav24", "wav32"}
	}
	return nil
}

func (m *wizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.cancel = true
			return m, tea.Quit
		case tea.KeyEnter:
			return m.advance()
		case tea.KeyUp, tea.KeyCtrlP:
			if m.isChoice(m.fields[m.current].kind) {
				m.step(-1)
			} else if m.current > 0 {
				m.current--
				m.syncFocus()
			}
			return m, nil
		case tea.KeyDown, tea.KeyCtrlN:
			if m.isChoice(m.fields[m.current].kind) {
				m.step(1)
			} else if m.current < len(m.fields)-1 {
				m.current++
				m.syncFocus()
			}
			return m, nil
		case tea.KeyLeft:
			if m.isChoice(m.fields[m.current].kind) {
				m.step(-1)
			}
			return m, nil
		case tea.KeyRight:
			if m.isChoice(m.fields[m.current].kind) {
				m.step(1)
			}
			return m, nil
		}
	}
	return m, nil
}

func (m *wizardModel) step(delta int) {
	spec := m.fields[m.current]
	choices := m.choicesFor(spec.kind)
	n := len(choices)
	if n == 0 {
		return
	}
	if spec.kind == fieldVocals {
		m.vocalIndex = (m.vocalIndex + delta + n) % n
		return
	}
	m.formatIdx = (m.formatIdx + delta + n) % n
}

func (m *wizardModel) syncFocus() {
	for i := range m.inputs {
		if m.isChoice(m.fields[i].kind) {
			m.inputs[i].Blur()
			continue
		}
		if i == m.current {
			m.inputs[i].Focus()
		} else {
			m.inputs[i].Blur()
		}
	}
	m.inputs[m.current].Focus()
}

// advance validates the current field and moves on, or finishes the wizard.
func (m *wizardModel) advance() (tea.Model, tea.Cmd) {
	spec := m.fields[m.current]
	if !m.isChoice(spec.kind) {
		if err := m.validateCurrent(); err != nil {
			m.err = err
			return m, nil
		}
	}
	m.err = nil
	if m.current < len(m.fields)-1 {
		m.current++
		m.syncFocus()
		return m, nil
	}
	m.collect()
	m.done = true
	return m, tea.Quit
}

func (m *wizardModel) validateCurrent() error {
	spec := m.fields[m.current]
	value := strings.TrimSpace(m.inputs[m.current].Value())
	switch spec.kind {
	case fieldCaption:
		if value == "" {
			return fmt.Errorf("a description is required")
		}
		if len(value) < 3 {
			return fmt.Errorf("describe the song in a few more words")
		}
	case fieldDuration:
		var d float64
		if _, err := fmt.Sscanf(value, "%g", &d); err != nil {
			return fmt.Errorf("duration must be a number of seconds")
		}
		if d < 0 || d > 600 {
			return fmt.Errorf("duration must be between 0 and 600 seconds")
		}
	}
	return nil
}

func (m *wizardModel) collect() {
	m.answer = Answer{
		Caption:      strings.TrimSpace(m.inputs[fieldCaption].Value()),
		Style:        strings.TrimSpace(m.inputs[fieldStyle].Value()),
		Lyrics:       strings.TrimSpace(m.inputs[fieldLyrics].Value()),
		Instrumental: m.vocalIndex == 1,
		Format:       m.choicesFor(fieldFormat)[m.formatIdx],
	}
	fmt.Sscanf(strings.TrimSpace(m.inputs[fieldDuration].Value()), "%g", &m.answer.Duration)
}

func (m *wizardModel) View() string {
	if m.done || m.cancel {
		return ""
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("acestep · new track"))
	b.WriteString("\n\n")

	// Only show a compact window around the active field.
	start := m.current - 2
	if start < 0 {
		start = 0
	}
	end := m.current + 3
	if end > len(m.fields) {
		end = len(m.fields)
	}

	for i := start; i < end; i++ {
		spec := m.fields[i]
		active := i == m.current
		marker := "  "
		if active {
			marker = "> "
		}
		b.WriteString(marker)
		b.WriteString(labelFor(spec))
		b.WriteString("\n")

		switch {
		case m.isChoice(spec.kind):
			choices := m.choicesFor(spec.kind)
			sel := m.vocalIndex
			if spec.kind == fieldFormat {
				sel = m.formatIdx
			}
			for ci, c := range choices {
				if ci == sel {
					b.WriteString("    " + selectedStyle.Render("["+c+"]"))
				} else {
					b.WriteString("    " + subtleStyle.Render(" "+c+" "))
				}
			}
		default:
			b.WriteString("    " + m.inputs[i].View())
		}
		if active && spec.hint != "" {
			b.WriteString("\n    " + subtleStyle.Render(spec.hint))
		}
		b.WriteString("\n\n")
	}

	if m.err != nil {
		b.WriteString(errorStyle.Render("! "+m.err.Error()) + "\n\n")
	}

	hint := "↑/↓ move · enter continue · ctrl+c cancel"
	if m.isChoice(m.fields[m.current].kind) {
		hint = "←/→ choose · enter continue · ctrl+c cancel"
	}
	b.WriteString(subtleStyle.Render(hint))
	return b.String()
}

func labelFor(f fieldSpec) string {
	return f.label
}

// RunWizard collects a generation request interactively. It returns
// (nil, nil) when the user cancels.
func RunWizard(initial Answer) (Answer, error) {
	m := newWizard()
	if initial.Caption != "" {
		m.inputs[fieldCaption].SetValue(initial.Caption)
	}
	if initial.Style != "" {
		m.inputs[fieldStyle].SetValue(initial.Style)
	}
	if initial.Lyrics != "" {
		m.inputs[fieldLyrics].SetValue(initial.Lyrics)
	}
	if initial.Duration > 0 {
		m.inputs[fieldDuration].SetValue(fmt.Sprintf("%g", initial.Duration))
	}
	if initial.Instrumental {
		m.vocalIndex = 1
	}
	for i, f := range m.choicesFor(fieldFormat) {
		if f == initial.Format {
			m.formatIdx = i
			break
		}
	}
	m.syncFocus()

	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return Answer{}, fmt.Errorf("interactive wizard: %w", err)
	}
	wm, ok := final.(*wizardModel)
	if !ok || wm.cancel || !wm.done {
		return Answer{}, nil
	}
	return wm.answer, nil
}
