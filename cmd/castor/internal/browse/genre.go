package browse

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/stupside/castor/cmd/castor/internal/browse/tmdb"
	"github.com/stupside/castor/cmd/castor/internal/palette"
)

// genrePicker is the modal genre filter; owns draft selection, isolated from feed.
type genrePicker struct {
	list    list.Model
	keys    genreKeys
	styles  styles
	catalog tmdb.GenreCatalog
	loaded  bool
	err     error
	applied genreSelection
	draft   *genreSelection // nil when the editor is closed
}

type genreSelection struct {
	media    tmdb.Media
	selected map[int]struct{}
}

func (s genreSelection) genreIDs() []int { return slices.Sorted(maps.Keys(s.selected)) }

func (s genreSelection) otherMedia() genreSelection {
	media := tmdb.MediaTV
	if s.media == tmdb.MediaTV {
		media = tmdb.MediaMovie
	}
	return genreSelection{media: media, selected: map[int]struct{}{}}
}

// genreKeys are the bindings only the genre overlay answers to.
type genreKeys struct{ space, clear, media key.Binding }

func defaultGenreKeys() genreKeys {
	return genreKeys{
		space: key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "toggle")),
		clear: key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "clear")),
		media: key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "movie/tv")),
	}
}

// genreAction is the result of a key press to the picker.
type genreAction int

const (
	genreIdle      genreAction = iota // still open; nothing for the model to do
	genreCancelled                    // closed without applying
	genreApplied                      // closed; run a discover query
)

func newGenrePicker() genrePicker {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(true)
	l.SetFilteringEnabled(false) // bare letters drive picker commands
	// DisableQuitKeybindings() survives state changes; SetEnabled() reverts.
	l.DisableQuitKeybindings()

	return genrePicker{
		list:    l,
		keys:    defaultGenreKeys(),
		applied: genreSelection{media: tmdb.MediaMovie, selected: map[int]struct{}{}},
	}
}

func (g *genrePicker) restyle(p palette.Palette, st styles) {
	g.styles = st
	g.list.SetDelegate(newGenreDelegate(p))
	p.StyleList(&g.list)
}

// setCatalog refreshes checklist if modal is open before fetch completes.
func (g *genrePicker) setCatalog(cat tmdb.GenreCatalog, w, h int) {
	g.catalog = cat
	g.loaded = true
	if g.draft != nil {
		g.resize(w, h) // open() skipped it: there was no catalogue to size the list against
		g.reload()
	}
}

func (g *genrePicker) open(w, h int) {
	g.draft = &genreSelection{media: g.applied.media, selected: maps.Clone(g.applied.selected)}
	if g.loaded {
		g.resize(w, h)
		g.reload()
	}
}

func (g *genrePicker) resize(w, h int) {
	rows := min(len(g.catalog.For(g.draft.media)), max(h-10, 6))
	width := min(max(w-8, 24), 48)
	g.list.SetSize(width, max(rows, 1))
}

func (g *genrePicker) update(msg tea.KeyPressMsg, keys keyMap, w, h int) (tea.Cmd, genreAction) {
	switch {
	case key.Matches(msg, keys.back):
		g.draft = nil
		return nil, genreCancelled
	case key.Matches(msg, keys.enter):
		if !g.loaded {
			return nil, genreIdle
		}
		g.applied, g.draft = *g.draft, nil
		return nil, genreApplied
	case key.Matches(msg, g.keys.space):
		if it, ok := g.list.SelectedItem().(genreItem); ok {
			selected := maps.Clone(g.draft.selected)
			if _, on := selected[it.g.ID]; on {
				delete(selected, it.g.ID)
			} else {
				selected[it.g.ID] = struct{}{}
			}
			g.draft = &genreSelection{media: g.draft.media, selected: selected}
			g.reload()
		}
		return nil, genreIdle
	case key.Matches(msg, g.keys.clear):
		g.draft = &genreSelection{media: g.draft.media, selected: map[int]struct{}{}}
		g.reload()
		return nil, genreIdle
	case key.Matches(msg, g.keys.media):
		g.toggleMedia(w, h)
		return nil, genreIdle
	}
	var cmd tea.Cmd
	g.list, cmd = g.list.Update(msg)
	return cmd, genreIdle
}

// toggleMedia clears the draft selection, because genre ids are namespaced per media type.
func (g *genrePicker) toggleMedia(w, h int) {
	next := g.draft.otherMedia()
	g.draft = &next
	if g.loaded {
		g.resize(w, h)
		g.reload()
	}
}

func (g *genrePicker) reload() {
	idx := g.list.Index()
	genres := g.catalog.For(g.draft.media)
	items := make([]list.Item, len(genres))
	for i, gr := range genres {
		_, on := g.draft.selected[gr.ID]
		items[i] = genreItem{g: gr, selected: on}
	}
	g.list.SetItems(items)
	// TV catalogue shorter; list.Select doesn't bound-check; clamp cursor.
	g.list.Select(min(idx, max(len(items)-1, 0)))
}

// selectedNames is in catalogue order.
func (g genrePicker) selectedNames() []string {
	var names []string
	for _, gr := range g.catalog.For(g.applied.media) {
		if _, on := g.applied.selected[gr.ID]; on {
			names = append(names, gr.Name)
		}
	}
	return names
}

func (s genreSelection) mediaLabel() string {
	if s.media == tmdb.MediaTV {
		return "TV Shows"
	}
	return "Movies"
}

func (g genrePicker) view(spin spinner.Model, hints help.Model, keys keyMap, w, h int) string {
	title := lipgloss.JoinHorizontal(lipgloss.Left,
		g.styles.titleText.Render("Filter by genre"),
		g.styles.muted.Render("  ·  "+g.draft.mediaLabel()),
	)

	body := g.styles.muted.Render(spin.View() + " loading genres…")
	switch {
	case g.loaded:
		body = g.list.View()
	case g.err != nil:
		body = "Could not load genres: " + g.err.Error() + "\nClose and reopen to retry."
	}

	count := "no genres selected"
	if n := len(g.draft.selected); n > 0 {
		count = fmt.Sprintf("%d selected", n)
	}

	apply, cancel := keys.enter, keys.back
	apply.SetHelp("↵", "apply")
	cancel.SetHelp("esc", "cancel")

	content := lipgloss.JoinVertical(lipgloss.Left,
		title, "", body, "",
		g.styles.muted.Render(count),
		hints.ShortHelpView([]key.Binding{g.keys.space, apply, g.keys.media, g.keys.clear, cancel}),
	)
	box := g.styles.box.Render(content)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}

type genreItem struct {
	g        tmdb.Genre
	selected bool
}

func (i genreItem) Title() string {
	box := "○"
	if i.selected {
		box = "◉"
	}
	return box + "  " + i.g.Name
}

func (i genreItem) Description() string { return "" }
func (i genreItem) FilterValue() string { return i.g.Name }

func newGenreDelegate(p palette.Palette) list.DefaultDelegate {
	d := p.Delegate()
	d.ShowDescription = false
	d.SetSpacing(0)
	return d
}

type genresLoadedMsg struct {
	cat tmdb.GenreCatalog
	err error
}

func loadGenresCmd(ctx context.Context, c Catalog) tea.Cmd {
	return func() tea.Msg {
		cat, err := c.Genres(ctx)
		return genresLoadedMsg{cat: cat, err: err}
	}
}
