package tui

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/bytenote/jmp/internal/store"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// ----- tech/cyber theme -----

var (
	cyan    = lipgloss.Color("#00F0FF")
	magenta = lipgloss.Color("#FF2AF1")
	green   = lipgloss.Color("#00FF9F")
	amber   = lipgloss.Color("#FFB800")
	red     = lipgloss.Color("#FF3366")
	dim     = lipgloss.Color("#4A5568")
	darkBg  = lipgloss.Color("#0D1117")

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(cyan)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#0D1117")).
			Background(cyan).
			Bold(true)

	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#C9D1D9"))

	dimStyle = lipgloss.NewStyle().
			Foreground(dim)

	scoreStyle = lipgloss.NewStyle().
			Foreground(green)

	starStyle = lipgloss.NewStyle().
			Foreground(amber)

	dangerStyle = lipgloss.NewStyle().
			Foreground(red).
			Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(dim).
			MarginTop(1)

	inputBoxStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.Border{
			Top: "─", Bottom: "─", Left: "│", Right: "│",
			TopLeft: "╭", TopRight: "╮", BottomLeft: "╰", BottomRight: "╯",
		}).
		BorderForeground(magenta).
		Padding(0, 1)

	statusStyle = lipgloss.NewStyle().
			Foreground(green)

	errStyle = lipgloss.NewStyle().
			Foreground(red)

	tagStyle = lipgloss.NewStyle().
			Foreground(darkBg).
			Background(magenta).
			Padding(0, 1)

	catStyle = lipgloss.NewStyle().
			Foreground(darkBg).
			Background(cyan).
			Padding(0, 1)

	labelStyle = lipgloss.NewStyle().
			Foreground(amber).
			Bold(true)

	promptStyle = lipgloss.NewStyle().
			Foreground(cyan).
			Bold(true)

	filterStyle = lipgloss.NewStyle().
			Foreground(amber).
			Bold(true)
)

func setStyleRenderer(r *lipgloss.Renderer) {
	headerStyle = headerStyle.Renderer(r)
	selectedStyle = selectedStyle.Renderer(r)
	normalStyle = normalStyle.Renderer(r)
	dimStyle = dimStyle.Renderer(r)
	scoreStyle = scoreStyle.Renderer(r)
	starStyle = starStyle.Renderer(r)
	dangerStyle = dangerStyle.Renderer(r)
	helpStyle = helpStyle.Renderer(r)
	inputBoxStyle = inputBoxStyle.Renderer(r)
	statusStyle = statusStyle.Renderer(r)
	errStyle = errStyle.Renderer(r)
	tagStyle = tagStyle.Renderer(r)
	catStyle = catStyle.Renderer(r)
	labelStyle = labelStyle.Renderer(r)
	promptStyle = promptStyle.Renderer(r)
	filterStyle = filterStyle.Renderer(r)
}

// ----- model -----

type mode int

const (
	modeList mode = iota
	modeSearch
	modeEdit
	modeAdd
	modeCategory
	modeConfirmDelete
)

// filterMode controls which entries are shown
type filterMode int

const (
	filterAll filterMode = iota
	filterRecent
	filterStarred
	filterCategory
)

type Model struct {
	st          *store.Store
	dbPath      string
	version     string
	entries     []*store.Entry
	cursor      int
	mode        mode
	filterMode  filterMode
	input       textinput.Model
	editPath    string
	status      string
	isErr       bool
	width       int
	height      int
	offset      int
	selected    string // path chosen by Enter, printed on exit
	showHelp    bool
	showDetail  bool
	showPreview bool
	marked      map[string]bool
}

func New(s *store.Store, dbPath, ver string) *Model {
	ti := textinput.New()
	ti.CharLimit = 512
	ti.Width = 60
	ti.PromptStyle = promptStyle
	ti.TextStyle = normalStyle

	m := &Model{
		st:      s,
		dbPath:  dbPath,
		version: ver,
		marked:  make(map[string]bool),
	}
	m.input = ti
	m.reload()
	return m
}

func (m *Model) reload() {
	switch m.filterMode {
	case filterRecent:
		all := m.st.All()
		m.entries = make([]*store.Entry, len(all))
		copy(m.entries, all)
		sortEntriesByRecent(m.entries)
	case filterStarred:
		all := m.st.All()
		m.entries = make([]*store.Entry, 0, len(all))
		for _, e := range all {
			if e.Starred {
				m.entries = append(m.entries, e)
			}
		}
	case filterCategory:
		all := m.st.All()
		m.entries = make([]*store.Entry, 0, len(all))
		for _, e := range all {
			if e.Category != "" {
				m.entries = append(m.entries, e)
			}
		}
	default:
		m.entries = m.st.All()
	}
	if m.cursor >= len(m.entries) && len(m.entries) > 0 {
		m.cursor = len(m.entries) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) filterEntries(q string) {
	if q == "" {
		m.reload()
		return
	}
	terms := strings.Fields(q)
	results := m.st.QueryEntries(terms)
	m.entries = results
	m.cursor = 0
	m.offset = 0
}

// ----- Init -----

func (m *Model) Init() tea.Cmd {
	return nil
}

// ----- Update -----

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c"))) {
		m.selected = ""
		return m, tea.Quit
	}

	switch m.mode {
	case modeList:
		return m.handleListKey(msg)
	case modeSearch:
		return m.handleSearchKey(msg)
	case modeEdit:
		return m.handleEditKey(msg)
	case modeAdd:
		return m.handleAddKey(msg)
	case modeCategory:
		return m.handleCategoryKey(msg)
	case modeConfirmDelete:
		return m.handleDeleteConfirmKey(msg)
	}
	return m, nil
}

func (m *Model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	visibleRows := m.visibleRows()
	switch msg.String() {
	case "q", "esc":
		m.selected = ""
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			if m.cursor < m.offset {
				m.offset = m.cursor
			}
		}
	case "down", "j":
		if m.cursor < len(m.entries)-1 {
			m.cursor++
			if m.cursor >= m.offset+visibleRows {
				m.offset = m.cursor - visibleRows + 1
			}
		}
	case "g":
		m.cursor = 0
		m.offset = 0
	case "G":
		m.cursor = len(m.entries) - 1
		if m.cursor >= visibleRows {
			m.offset = m.cursor - visibleRows + 1
		}
	case "enter":
		if len(m.entries) > 0 {
			m.selected = m.entries[m.cursor].Path
			return m, tea.Quit
		}
	case " ":
		m.toggleMark()
	case "?":
		m.showHelp = !m.showHelp
	case "i":
		m.showDetail = !m.showDetail
	case "p":
		m.showPreview = !m.showPreview
	case "/":
		m.mode = modeSearch
		m.input.SetValue("")
		m.input.Placeholder = "search query..."
		m.input.Focus()
		m.clearStatus()
	case "a":
		m.mode = modeAdd
		m.input.SetValue("")
		m.input.Placeholder = "/path/to/dir"
		m.input.Focus()
		m.clearStatus()
	case "e":
		if len(m.entries) > 0 {
			e := m.entries[m.cursor]
			m.editPath = e.Path
			m.mode = modeEdit
			m.input.SetValue(fmt.Sprintf("%.1f", e.Weight))
			m.input.Placeholder = "new weight..."
			m.input.Focus()
			m.clearStatus()
		}
	case "s":
		if len(m.entries) > 0 {
			paths := m.targetPaths()
			for _, path := range paths {
				m.st.ToggleStar(path)
			}
			if err := m.st.Save(); err != nil {
				m.setStatus("SAVE FAILED", true)
			} else {
				m.setStatus(fmt.Sprintf("TOGGLED %d", len(paths)), false)
			}
			m.clearMarks()
			m.reload()
		}
	case "c":
		if len(m.entries) > 0 {
			e := m.entries[m.cursor]
			m.editPath = e.Path
			m.mode = modeCategory
			m.input.SetValue(e.Category)
			if len(m.marked) > 0 {
				m.input.SetValue("")
			}
			m.input.Placeholder = "category name..."
			m.input.Focus()
			m.clearStatus()
		}
	case "d", "delete":
		if len(m.entries) > 0 {
			m.mode = modeConfirmDelete
			m.clearStatus()
		}
	case "1":
		m.filterMode = filterAll
		m.cursor = 0
		m.offset = 0
		m.reload()
		m.setStatus("ALL", false)
	case "2":
		m.filterMode = filterRecent
		m.cursor = 0
		m.offset = 0
		m.reload()
		m.setStatus("RECENT", false)
	case "3":
		m.filterMode = filterStarred
		m.cursor = 0
		m.offset = 0
		m.reload()
		m.setStatus("STARRED", false)
	case "4":
		m.filterMode = filterCategory
		m.cursor = 0
		m.offset = 0
		m.reload()
		m.setStatus("CATEGORIZED", false)
	case "r":
		m.reload()
		m.setStatus("SYNCED", false)
	}
	return m, nil
}

func (m *Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.input.Blur()
		m.reload()
		m.clearStatus()
		return m, nil
	case "enter":
		if len(m.entries) > 0 {
			m.selected = m.entries[m.cursor].Path
			return m, tea.Quit
		}
		m.mode = modeList
		m.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.filterEntries(m.input.Value())
	return m, cmd
}

func (m *Model) handleEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.input.Blur()
		m.clearStatus()
		return m, nil
	case "enter":
		val := strings.TrimSpace(m.input.Value())
		w, err := strconv.ParseFloat(val, 64)
		if err != nil || w < 0 {
			m.setStatus("INVALID WEIGHT", true)
			m.mode = modeList
			m.input.Blur()
			return m, nil
		}
		m.st.SetWeight(m.editPath, w)
		if err := m.st.Save(); err != nil {
			m.setStatus("SAVE FAILED", true)
		} else {
			m.setStatus(fmt.Sprintf("UPDATED %.1f", w), false)
		}
		m.mode = modeList
		m.input.Blur()
		m.reload()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) handleAddKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.input.Blur()
		m.clearStatus()
		return m, nil
	case "enter":
		path := strings.TrimSpace(m.input.Value())
		if path == "" {
			m.mode = modeList
			m.input.Blur()
			return m, nil
		}
		if _, err := os.Stat(path); os.IsNotExist(err) {
			m.setStatus("NOT FOUND: "+path, true)
			m.mode = modeList
			m.input.Blur()
			return m, nil
		}
		m.st.AddManual(path, 10.0)
		if err := m.st.Save(); err != nil {
			m.setStatus("SAVE FAILED", true)
		} else {
			m.setStatus("ADDED", false)
		}
		m.mode = modeList
		m.input.Blur()
		m.reload()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) handleCategoryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.input.Blur()
		m.clearStatus()
		return m, nil
	case "enter":
		cat := strings.TrimSpace(m.input.Value())
		for _, path := range m.targetPathsForEdit() {
			m.st.SetCategory(path, cat)
		}
		if err := m.st.Save(); err != nil {
			m.setStatus("SAVE FAILED", true)
		} else {
			if cat == "" {
				m.setStatus("CATEGORY CLEARED", false)
			} else {
				m.setStatus("CATEGORY: "+cat, false)
			}
		}
		m.mode = modeList
		m.input.Blur()
		m.clearMarks()
		m.reload()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) handleDeleteConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		if len(m.entries) > 0 {
			paths := m.targetPaths()
			for _, path := range paths {
				m.st.Remove(path)
			}
			if err := m.st.Save(); err != nil {
				m.setStatus("SAVE FAILED", true)
			} else {
				m.setStatus(fmt.Sprintf("DELETED %d", len(paths)), false)
			}
			m.clearMarks()
			m.reload()
		}
		m.mode = modeList
	case "n", "N", "esc":
		m.mode = modeList
		m.clearStatus()
	}
	return m, nil
}

// ----- View -----

func (m *Model) View() string {
	var sb strings.Builder
	w := m.width
	if w < 60 {
		w = 60
	}

	// Title line
	sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cyan).Render(" JMP ") + " ")
	sb.WriteString(dimStyle.Render("path jumper " + m.version))

	// Filter tabs
	filters := []struct {
		key   string
		label string
		mode  filterMode
	}{
		{"1", "ALL", filterAll},
		{"2", "RECENT", filterRecent},
		{"3", "STARRED", filterStarred},
		{"4", "CATEGORIZED", filterCategory},
	}
	for _, f := range filters {
		sb.WriteString(" ")
		if m.filterMode == f.mode {
			sb.WriteString(tagStyle.Render(f.key + ":" + f.label))
		} else {
			sb.WriteString(dimStyle.Render(f.key + ":" + f.label))
		}
	}

	count := fmt.Sprintf("%d", len(m.entries))
	if len(m.marked) > 0 {
		count += fmt.Sprintf("  marked:%d", len(m.marked))
	}
	sb.WriteString("  " + dimStyle.Render(count) + "\n")

	// Separator
	sb.WriteString(dimStyle.Render(strings.Repeat("─", w-2)) + "\n")

	// Column headers
	sb.WriteString("  " + headerStyle.Width(7).Render("SCORE") + " ")
	sb.WriteString(headerStyle.Width(5).Render("HITS") + " ")
	sb.WriteString(headerStyle.Width(1).Render("★") + " ")
	sb.WriteString(headerStyle.Width(10).Render("CAT") + " ")
	sb.WriteString(headerStyle.Render("PATH"))
	sb.WriteString("\n")

	// Entry list
	visibleRows := m.visibleRows()
	for i := m.offset; i < m.offset+visibleRows && i < len(m.entries); i++ {
		sb.WriteString(m.renderRow(i) + "\n")
	}

	// Scroll indicator
	if len(m.entries) > visibleRows {
		shown := min(m.offset+visibleRows, len(m.entries))
		pct := shown * 100 / len(m.entries)
		sb.WriteString(dimStyle.Render(fmt.Sprintf("  ── [%d-%d / %d] %d%% ──", m.offset+1, shown, len(m.entries), pct)))
	}

	// Status / confirm
	sb.WriteString("\n")
	if m.mode == modeConfirmDelete && len(m.entries) > 0 {
		sb.WriteString(dangerStyle.Render(fmt.Sprintf(" DELETE %d item(s) ? [y/n]", len(m.targetPaths()))))
	} else if m.status != "" {
		icon := "●"
		style := statusStyle
		if m.isErr {
			icon = "✖"
			style = errStyle
		}
		sb.WriteString(style.Render(fmt.Sprintf(" %s %s", icon, m.status)))
	}

	// Input box
	if m.mode == modeSearch || m.mode == modeEdit || m.mode == modeAdd || m.mode == modeCategory {
		label := map[mode]string{
			modeSearch:   "SEARCH",
			modeEdit:     "WEIGHT",
			modeAdd:      "ADD PATH",
			modeCategory: "CATEGORY",
		}[m.mode]
		sb.WriteString("\n  " + labelStyle.Render("▸ "+label) + "\n")
		sb.WriteString("  " + inputBoxStyle.Render(m.input.View()))
	}

	if m.showDetail {
		sb.WriteString("\n" + m.renderDetail())
	}
	if m.showPreview {
		sb.WriteString("\n" + m.renderPreview())
	}
	if m.showHelp {
		sb.WriteString("\n" + m.renderHelpPanel())
	}

	// Help bar
	sb.WriteString(m.renderHelp())

	return sb.String()
}

func (m *Model) renderRow(i int) string {
	e := m.entries[i]
	score := fmt.Sprintf("%6.1f", e.Frecency())
	visits := fmt.Sprintf("%4d", e.Visits)
	path := e.Path

	star := " "
	if e.Starred {
		star = "★"
	}
	mark := " "
	if m.marked[e.Path] {
		mark = "✓"
	}

	cat := ""
	if e.Category != "" {
		if len(e.Category) > 8 {
			cat = e.Category[:8]
		} else {
			cat = e.Category
		}
	}

	maxPath := m.width - 42
	if maxPath < 20 {
		maxPath = 20
	}
	if len(path) > maxPath {
		path = "…" + path[len(path)-maxPath+1:]
	}

	if i == m.cursor {
		row := fmt.Sprintf("▸%s %s %s %s %-8s %s", mark, score, visits, star, cat, path)
		return selectedStyle.Render(" " + row + strings.Repeat(" ", max(0, m.width-len(row)-4)))
	}

	starRendered := dimStyle.Render(star)
	if e.Starred {
		starRendered = starStyle.Render(star)
	}

	catRendered := dimStyle.Width(8).Render(cat)
	if cat != "" {
		catRendered = lipgloss.NewStyle().Foreground(cyan).Width(8).Render(cat)
	}

	return fmt.Sprintf(" %s %s %s %s %s %s",
		dimStyle.Render(mark),
		scoreStyle.Width(6).Render(score),
		dimStyle.Width(4).Render(visits),
		starRendered,
		catRendered,
		normalStyle.Render(path),
	)
}

func (m *Model) renderHelp() string {
	keys := []string{
		promptStyle.Render("↑/↓") + " move",
		promptStyle.Render("↵") + " enter",
		promptStyle.Render("/") + " find",
		promptStyle.Render("space") + " mark",
		promptStyle.Render("?") + " help",
		promptStyle.Render("q") + " quit",
	}
	return helpStyle.Render("\n  " + strings.Join(keys, "  "+dimStyle.Render("│")+"  "))
}

func (m *Model) renderDetail() string {
	if len(m.entries) == 0 {
		return ""
	}
	e := m.entries[m.cursor]
	aliases := strings.Join(m.st.AliasesForPath(e.Path), " ")
	if aliases == "" {
		aliases = "-"
	}
	category := e.Category
	if category == "" {
		category = "-"
	}
	lines := []string{
		labelStyle.Render("DETAIL"),
		fmt.Sprintf("path: %s", e.Path),
		fmt.Sprintf("score: %.2f  weight: %.2f  visits: %d", e.Frecency(), e.Weight, e.Visits),
		fmt.Sprintf("last: %s  star: %t  category: %s  aliases: %s", e.LastVisit.Format("2006-01-02 15:04"), e.Starred, category, aliases),
	}
	return dimStyle.Render("  " + strings.Join(lines, "\n  "))
}

func (m *Model) renderPreview() string {
	if len(m.entries) == 0 {
		return ""
	}
	path := m.entries[m.cursor].Path
	items, err := os.ReadDir(path)
	if err != nil {
		return errStyle.Render("  PREVIEW unavailable: " + err.Error())
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDir() != items[j].IsDir() {
			return items[i].IsDir()
		}
		return items[i].Name() < items[j].Name()
	})
	limit := 5
	if len(items) < limit {
		limit = len(items)
	}
	lines := []string{labelStyle.Render("PREVIEW")}
	for i := 0; i < limit; i++ {
		name := items[i].Name()
		if items[i].IsDir() {
			name += string(os.PathSeparator)
		}
		lines = append(lines, name)
	}
	if len(items) > limit {
		lines = append(lines, fmt.Sprintf("... %d more", len(items)-limit))
	}
	return dimStyle.Render("  " + strings.Join(lines, "\n  "))
}

func (m *Model) renderHelpPanel() string {
	lines := []string{
		labelStyle.Render("HELP"),
		"1 all  2 recent  3 starred  4 categorized",
		"space mark  s star  c category  d delete  a add  e weight",
		"/ search  i detail  p preview  r reload",
		"enter jump  q quit",
		"marked rows are used by star/category/delete",
	}
	return dimStyle.Render("  " + strings.Join(lines, "\n  "))
}

func (m *Model) toggleMark() {
	if len(m.entries) == 0 {
		return
	}
	path := m.entries[m.cursor].Path
	if m.marked[path] {
		delete(m.marked, path)
	} else {
		m.marked[path] = true
	}
}

func (m *Model) targetPaths() []string {
	if len(m.marked) == 0 {
		if len(m.entries) == 0 {
			return nil
		}
		return []string{m.entries[m.cursor].Path}
	}
	paths := make([]string, 0, len(m.marked))
	for path := range m.marked {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func (m *Model) targetPathsForEdit() []string {
	if len(m.marked) > 0 {
		return m.targetPaths()
	}
	if m.editPath == "" {
		return nil
	}
	return []string{m.editPath}
}

func (m *Model) clearMarks() {
	m.marked = make(map[string]bool)
}

func (m *Model) visibleRows() int {
	reserved := 10
	if m.mode == modeSearch || m.mode == modeEdit || m.mode == modeAdd || m.mode == modeCategory {
		reserved += 4
	}
	if m.showDetail {
		reserved += 5
	}
	if m.showPreview {
		reserved += 6
	}
	if m.showHelp {
		reserved += 7
	}
	rows := m.height - reserved
	if rows < 5 {
		return 5
	}
	return rows
}

func (m *Model) setStatus(s string, isErr bool) {
	m.status = s
	m.isErr = isErr
}

func (m *Model) clearStatus() {
	m.status = ""
	m.isErr = false
}

// Run launches the TUI. Returns the selected path, or empty string if quit.
func Run(s *store.Store, dbPath, colorMode, ver string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		tty = os.Stderr
	} else {
		defer tty.Close()
	}
	renderer := lipgloss.NewRenderer(tty)
	applyColorMode(renderer, colorMode)
	lipgloss.SetDefaultRenderer(renderer)
	setStyleRenderer(renderer)
	m := New(s, dbPath, ver)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithInput(tty), tea.WithOutput(tty))
	_, err = p.Run()
	return m.selected, err
}

func applyColorMode(r *lipgloss.Renderer, mode string) {
	switch mode {
	case "never":
		r.SetColorProfile(termenv.Ascii)
	case "ansi256":
		r.SetColorProfile(termenv.ANSI256)
	case "always", "truecolor":
		r.SetColorProfile(termenv.TrueColor)
	case "auto", "":
		r.SetColorProfile(termenv.TrueColor)
	default:
		r.SetColorProfile(termenv.TrueColor)
	}
}

func sortEntriesByRecent(entries []*store.Entry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].LastVisit.After(entries[j].LastVisit)
	})
}
