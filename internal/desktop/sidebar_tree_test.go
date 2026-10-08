package desktop

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// treeApp has a folder holding one request, and three requests after it.
func treeApp(t *testing.T) (*App, *engine.Engine, map[string]string) {
	t.Helper()
	a, e := cookieApp(t)
	wid := a.workspace
	ids := map[string]string{}
	save := func(m engine.Object) {
		m["workspaceId"] = wid
		saved, err := e.Save(t.Context(), m)
		if err != nil {
			t.Fatal(err)
		}
		a.applyModel(saved)
		ids[s(saved, "name")] = s(saved, "id")
	}
	save(engine.Object{"model": "folder", "name": "Folder", "sortPriority": 0})
	save(engine.Object{"model": "http_request", "name": "Inner", "folderId": ids["Folder"], "sortPriority": 0})
	save(engine.Object{"model": "http_request", "name": "Alpha", "sortPriority": 1000})
	save(engine.Object{"model": "http_request", "name": "Beta", "sortPriority": 2000})
	save(engine.Object{"model": "http_request", "name": "Gamma", "sortPriority": 3000})
	return a, e, ids
}

func treeNames(a *App, parent string) []string {
	var names []string
	for _, m := range a.treeItems(parent) {
		names = append(names, s(m, "name"))
	}
	return names
}

func TestSidebarSelectionWithShiftAndCmd(t *testing.T) {
	a, _, ids := treeApp(t)
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Alpha"); err != nil {
		t.Fatal(err)
	}
	if a.active != ids["Alpha"] || !slices.Equal(a.tree.selected, []string{ids["Alpha"]}) {
		t.Fatal(a.active, a.tree.selected)
	}
	if err := tt.ClickWith(ui.Shift, "Gamma"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.tree.selected, []string{ids["Alpha"], ids["Beta"], ids["Gamma"]}) || a.active != ids["Alpha"] {
		t.Fatal(a.tree.selected)
	}
	if err := tt.ClickWith(ui.Cmd, "Beta"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.tree.selected, []string{ids["Alpha"], ids["Gamma"]}) {
		t.Fatal(a.tree.selected)
	}
	// A folder's row selects it; its chevron opens it.
	if err := tt.Click("Folder"); err != nil {
		t.Fatal(err)
	}
	if a.expanded[ids["Folder"]] || tt.HasText("Inner") {
		t.Fatal("folder opened on click")
	}
	if err := tt.Click("Toggle folder"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Inner") {
		t.Fatal(tt.Texts())
	}
}

func TestSidebarKeyboardNavigationAndInlineRename(t *testing.T) {
	a, e, ids := treeApp(t)
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Alpha"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(ui.Shift, ui.KeyDown)
	if !slices.Equal(a.tree.selected, []string{ids["Beta"], ids["Gamma"]}) {
		t.Fatal(a.tree.selected)
	}
	tt.Key(0, ui.KeyUp)
	tt.Key(0, ui.KeyUp)
	tt.Key(0, ui.KeyUp)
	if a.tree.last != ids["Folder"] {
		t.Fatal(a.tree.last)
	}
	tt.Key(0, ui.KeyRight)
	if !a.expanded[ids["Folder"]] {
		t.Fatal("ArrowRight did not open the folder")
	}
	tt.Key(0, ui.KeyRight)
	if a.tree.last != ids["Inner"] {
		t.Fatal(a.tree.last)
	}
	tt.Key(0, ui.KeyLeft)
	if a.tree.last != ids["Folder"] {
		t.Fatal(a.tree.last)
	}
	tt.Key(0, ui.KeyEnter)
	if a.tree.renaming != ids["Folder"] {
		t.Fatal("Enter did not rename")
	}
	tt.Frame()
	tt.Type("Renamed")
	tt.Key(0, ui.KeyEnter)
	m, err := e.Store.Get(t.Context(), ids["Folder"])
	if err != nil || s(m, "name") != "Renamed" || a.tree.renaming != "" {
		t.Fatal(m, err, a.tree.renaming)
	}
	// Escape gives up a rename.
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Type("Nope")
	tt.Key(0, ui.KeyEscape)
	if m, _ = e.Store.Get(t.Context(), ids["Folder"]); s(m, "name") != "Renamed" || a.tree.renaming != "" {
		t.Fatal(m)
	}
}

func TestSidebarDeleteSelected(t *testing.T) {
	a, e, ids := treeApp(t)
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Alpha"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ClickWith(ui.Shift, "Beta"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDelete)
	if !a.dialogOpen || a.dialogTitle != "Delete 2 Items" || !tt.HasText("Permanently delete the following?") {
		t.Fatal(a.dialogTitle, tt.Texts())
	}
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Alpha", "Beta"} {
		if _, err := e.Store.Get(t.Context(), ids[name]); err == nil {
			t.Fatal(name, "not deleted")
		}
	}
	if !slices.Equal(treeNames(a, ""), []string{"Folder", "Gamma"}) {
		t.Fatal(treeNames(a, ""))
	}
}

func TestSidebarMoveItemsKeepsYaakSortPriorities(t *testing.T) {
	a, e, ids := treeApp(t)
	// Between two rows with room: the moved rows fit between their neighbors.
	a.moveTreeItems([]string{ids["Gamma"]}, "", 1)
	if !slices.Equal(treeNames(a, ""), []string{"Folder", "Gamma", "Alpha", "Beta"}) {
		t.Fatal(treeNames(a, ""))
	}
	if p := n(a.models[ids["Gamma"]], "sortPriority"); p <= 0 || p >= 1000 {
		t.Fatal(p)
	}
	// Into a folder, as its first child.
	a.dropTreeItems([]string{ids["Alpha"], ids["Beta"]}, a.models[ids["Folder"]], dropAfter)
	if !slices.Equal(treeNames(a, ids["Folder"]), []string{"Alpha", "Beta", "Inner"}) {
		t.Fatal(treeNames(a, ids["Folder"]))
	}
	stored, err := e.Store.Get(t.Context(), ids["Beta"])
	if err != nil || s(stored, "folderId") != ids["Folder"] {
		t.Fatal(stored, err)
	}
	// Back to the root, at its end.
	a.moveTreeItems([]string{ids["Inner"]}, "", len(a.treeItems("")))
	if !slices.Equal(treeNames(a, ""), []string{"Folder", "Gamma", "Inner"}) || a.models[ids["Inner"]]["folderId"] != nil {
		t.Fatal(treeNames(a, ""), a.models[ids["Inner"]])
	}
	// Never into itself.
	a.moveTreeItems([]string{ids["Folder"]}, ids["Folder"], 0)
	if s(a.models[ids["Folder"]], "folderId") != "" {
		t.Fatal(a.models[ids["Folder"]])
	}
}

func TestSidebarDragReordersRows(t *testing.T) {
	a, _, ids := treeApp(t)
	tt := ui.NewTester(a.View, 1360, 860)
	from, _ := tt.Find("Gamma")
	to, _ := tt.Find("Alpha")
	tt.Press(from.X+5, from.Y+from.H/2)
	tt.Move(from.X+5, from.Y-10)
	tt.Move(to.X+5, to.Y+2)
	tt.Release(to.X+5, to.Y+2)
	if !slices.Equal(treeNames(a, ""), []string{"Folder", "Gamma", "Alpha", "Beta"}) {
		t.Fatal(treeNames(a, ""), ids)
	}
}

func TestFolderPageAfterDuplicatingFolder(t *testing.T) {
	a, _, ids := treeApp(t)
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.RightClick("Folder"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Duplicate"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.activeFolder == "" || a.activeFolder == ids["Folder"] || !tt.HasText("Send All") || !tt.HasText("No Responses") {
		t.Fatal(a.activeFolder, tt.Texts())
	}
	if err := tt.Click("Open Inner"); err != nil {
		t.Fatal(err)
	}
	if a.activeFolder != "" || s(a.models[a.active], "name") != "Inner" {
		t.Fatal(a.activeFolder, a.active)
	}
}

func TestMoveRequestsToWorkspaceAndSwitchBehavior(t *testing.T) {
	a, e, ids := treeApp(t)
	other, err := e.Save(t.Context(), engine.Object{"model": "workspace", "name": "Other"})
	if err != nil {
		t.Fatal(err)
	}
	a.applyModel(other)
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Alpha"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ClickWith(ui.Shift, "Beta"); err != nil {
		t.Fatal(err)
	}
	if err := tt.RightClick("Beta"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Move 2 Requests"); err != nil {
		t.Fatal(tt.Menu())
	}
	if a.dialogTitle != "Move 2 Requests" {
		t.Fatal(a.dialogTitle)
	}
	if err := tt.Click("Cookie tests (current)"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Other"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Confirm Move 2 Requests"); err != nil {
		t.Fatal(err)
	}
	stored, _ := e.Store.Get(t.Context(), ids["Beta"])
	if s(stored, "workspaceId") != s(other, "id") || slices.Contains(treeNames(a, ""), "Alpha") || !tt.HasText("2 requests moved to Other") {
		t.Fatal(stored, treeNames(a, ""), tt.Texts())
	}

	// Choosing another workspace asks where to open it, unless remembered.
	opened := ""
	a.OpenWorkspace = func(id string) { opened = id }
	a.openWorkspace(s(other, "id"))
	tt.Frame()
	if !tt.HasText("Where would you like to open") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Remember my choice"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("New Window"); err != nil {
		t.Fatal(err)
	}
	if opened != s(other, "id") || a.settings["openWorkspaceNewWindow"] != true {
		t.Fatal(opened, a.settings["openWorkspaceNewWindow"])
	}
	opened = ""
	a.openWorkspace(s(other, "id"))
	if opened != s(other, "id") || a.dialogOpen {
		t.Fatal("remembered choice not used")
	}
}
