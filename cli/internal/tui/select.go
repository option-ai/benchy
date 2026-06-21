// Package tui holds the interactive selection screens for `benchy run`: pick the
// evals, pick the models (filtered to installed agents), pick the judge.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Item is one selectable row.
type Item struct {
	Label string
	Desc  string
}

// maxVisible caps the window even on tall terminals: scanning beats scrolling
// a wall of options.
const maxVisible = 12

type selectModel struct {
	title    string
	items    []Item
	cursor   int
	offset   int // first visible row
	visible  int // rows shown at once
	width    int // terminal width; rows are clipped so they never wrap
	chosen   map[int]bool
	multi    bool
	done     bool
	canceled bool
	crumbs   bool // show the wizard breadcrumb header
	step     int  // breadcrumb index (StepEvals…) when crumbs is set
	canBack  bool // q/esc goes back a step instead of cancelling
	back     bool // user asked to go back
}

func (m selectModel) Init() tea.Cmd { return nil }

// clampWindow keeps the cursor inside the visible window.
func (m *selectModel) clampWindow() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.visible {
		m.offset = m.cursor - m.visible + 1
	}
	if max := len(m.items) - m.visible; m.offset > max {
		m.offset = max
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		// leave room for title, indicators, and help text
		chrome := 6
		if m.crumbs {
			chrome = 8 // breadcrumb + spacer
		}
		m.visible = msg.Height - chrome
		if m.visible > maxVisible {
			m.visible = maxVisible
		}
		if m.visible < 3 {
			m.visible = 3
		}
		m.clampWindow()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.canceled = true
			return m, tea.Quit
		case "q", "esc":
			if m.canBack {
				m.back = true
			} else {
				m.canceled = true
			}
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "pgup":
			m.cursor -= m.visible
			if m.cursor < 0 {
				m.cursor = 0
			}
		case "pgdown":
			m.cursor += m.visible
			if m.cursor > len(m.items)-1 {
				m.cursor = len(m.items) - 1
			}
		case "home", "g":
			m.cursor = 0
		case "end", "G":
			m.cursor = len(m.items) - 1
		case " ":
			if m.multi {
				m.chosen[m.cursor] = !m.chosen[m.cursor]
			}
		case "a":
			if m.multi {
				all := len(m.chosen) < len(m.items)
				for i := range m.items {
					m.chosen[i] = all
				}
				if !all {
					m.chosen = map[int]bool{}
				}
			}
		case "enter":
			if !m.multi {
				m.chosen = map[int]bool{m.cursor: true}
			}
			if len(m.chosen) == 0 {
				return m, nil // require at least one
			}
			m.done = true
			return m, tea.Quit
		}
		m.clampWindow()
	}
	return m, nil
}

func (m selectModel) View() string {
	if m.done || m.canceled || m.back {
		return ""
	}
	var b strings.Builder
	if m.crumbs {
		b.WriteString(Crumbs(m.step) + "\n\n")
	}
	title := m.title
	if m.multi {
		n := 0
		for _, v := range m.chosen {
			if v {
				n++
			}
		}
		title += stDim.Render(fmt.Sprintf("  (%d/%d selected)", n, len(m.items)))
	}
	b.WriteString(stTitle.Render(title) + "\n\n")

	end := m.offset + m.visible
	if end > len(m.items) {
		end = len(m.items)
	}
	if m.offset > 0 {
		b.WriteString(stDim.Render(fmt.Sprintf("  ↑ %d more", m.offset)) + "\n")
	}
	for i := m.offset; i < end; i++ {
		it := m.items[i]
		cursor := "  "
		if i == m.cursor {
			cursor = stPick.Render("▸ ")
		}
		mark := " "
		if m.multi {
			if m.chosen[i] {
				mark = stSelected.Render("◉")
			} else {
				mark = "◯"
			}
		} else if i == m.cursor {
			mark = stPick.Render("•")
		}
		line := fmt.Sprintf("%s%s %s", cursor, mark, it.Label)
		if it.Desc != "" {
			line += "  " + stDim.Render(it.Desc)
		}
		b.WriteString(clip(line, m.width) + "\n")
	}
	if rest := len(m.items) - end; rest > 0 {
		b.WriteString(stDim.Render(fmt.Sprintf("  ↓ %d more", rest)) + "\n")
	}

	qHint := "q cancel"
	if m.canBack {
		qHint = "q back"
	}
	hint := "↑/↓ move · pgup/pgdn jump · enter confirm · " + qHint
	if m.multi {
		hint = "↑/↓ move · space toggle · a all · pgup/pgdn jump · enter confirm · " + qHint
	}
	b.WriteString(stHelp.Render(hint))
	return b.String()
}

// PickMany shows a multi-select and returns the chosen indices.
func PickMany(title string, items []Item) ([]int, error) {
	idxs, _, err := runSelect(title, items, true, false, 0, nil, false)
	return idxs, err
}

// PickOne shows a single-select and returns the chosen index.
func PickOne(title string, items []Item) (int, error) {
	idxs, _, err := runSelect(title, items, false, false, 0, nil, false)
	if err != nil {
		return -1, err
	}
	return idxs[0], nil
}

// PickStep shows one wizard step (breadcrumb header, demo-style). prev seeds
// the selection so going back preserves choices; canBack rebinds q/esc from
// cancel to "go back one step" (back=true in the return). ctrl+c always
// cancels.
func PickStep(title string, items []Item, multi bool, step int, prev []int, canBack bool) (idxs []int, back bool, err error) {
	chosen := map[int]bool{}
	for _, i := range prev {
		if i >= 0 && i < len(items) {
			chosen[i] = true
		}
	}
	return runSelect(title, items, multi, true, step, chosen, canBack)
}

func runSelect(title string, items []Item, multi bool, crumbs bool, step int, chosen map[int]bool, canBack bool) ([]int, bool, error) {
	if len(items) == 0 {
		return nil, false, fmt.Errorf("nothing to select")
	}
	if chosen == nil {
		chosen = map[int]bool{}
	}
	m := selectModel{
		title:   title,
		items:   items,
		chosen:  chosen,
		multi:   multi,
		visible: maxVisible,
		crumbs:  crumbs,
		step:    step,
		canBack: canBack,
	}
	out, err := tea.NewProgram(m).Run()
	if err != nil {
		return nil, false, err
	}
	fm := out.(selectModel)
	if fm.canceled {
		return nil, false, fmt.Errorf("canceled")
	}
	if fm.back {
		return nil, true, nil
	}
	var idxs []int
	for i := range items {
		if fm.chosen[i] {
			idxs = append(idxs, i)
		}
	}
	return idxs, false, nil
}
