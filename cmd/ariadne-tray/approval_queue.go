package main

import (
	"ariadne/internal/approval"
	"ariadne/internal/i18n"
	"fmt"
	"sort"
	"strings"

	"fyne.io/systray"
)

const approvalHistoryPageSize = 10

type approvalReadOnlyRow struct {
	row       *systray.MenuItem
	details   []*systray.MenuItem
	signature string
}

type approvalMenuEntry struct {
	view          approvalReadOnlyRow
	approve, deny *systray.MenuItem
	visible       bool
}

type approvalHistoryPage struct {
	menu *systray.MenuItem
	rows []approvalReadOnlyRow
}

var (
	approvalQueueMenu   *systray.MenuItem
	approvalHistoryMenu *systray.MenuItem
	approvalQueueItems  = make(map[string]approvalMenuEntry)
	approvalPages       []approvalHistoryPage
)

// Pending controls retain an exact ID. History slots are strictly read-only,
// so sorting a new record into the first page can never retarget an approval.
// Called with mu held.
func refreshApprovalQueueLocked(entries []approval.Entry, err error) {
	if approvalQueueMenu == nil || approvalHistoryMenu == nil {
		return
	}
	if err != nil {
		approvalQueueMenu.SetTitle(i18n.T(lang, "row.approvals") + ": " + i18n.T(lang, "notify.failed"))
		approvalQueueMenu.Disable()
		approvalHistoryMenu.Disable()
		for _, item := range approvalQueueItems {
			item.approve.Disable()
			item.deny.Disable()
		}
		return
	}
	pending, pages := approvalQueueSections(entries)
	if len(pending) == 0 {
		approvalQueueMenu.SetTitle(i18n.T(lang, "approval.none_pending"))
		approvalQueueMenu.Disable()
	} else {
		approvalQueueMenu.SetTitle(fmt.Sprintf(i18n.T(lang, "approval.pending_menu"), len(pending)))
		approvalQueueMenu.Enable()
	}
	active := make(map[string]bool, len(pending))
	for _, entry := range pending {
		id := entry.Request.ID
		active[id] = true
		item, exists := approvalQueueItems[id]
		if !exists {
			item.view.row = approvalQueueMenu.AddSubMenuItem("", "")
			updateApprovalRow(&item.view, entry, false)
			item.approve = item.view.row.AddSubMenuItem("", "")
			item.deny = item.view.row.AddSubMenuItem("", "")
			go handleApprovalMenu(id, item)
		}
		updateApprovalRow(&item.view, entry, false)
		item.approve.SetTitle(i18n.T(lang, "menu.approve"))
		item.deny.SetTitle(i18n.T(lang, "menu.deny"))
		item.approve.Enable()
		item.deny.Enable()
		if !item.visible {
			item.view.row.Show()
		}
		item.visible = true
		approvalQueueItems[id] = item
	}
	for id, item := range approvalQueueItems {
		if !active[id] && item.visible {
			item.approve.Disable()
			item.deny.Disable()
			item.view.row.Hide()
			item.visible = false
			approvalQueueItems[id] = item
		}
	}
	refreshApprovalHistoryLocked(pages, len(entries)-len(pending))
}

func refreshApprovalHistoryLocked(pages [][]approval.Entry, count int) {
	approvalHistoryMenu.SetTitle(fmt.Sprintf(i18n.T(lang, "approval.history_menu"), count))
	if count == 0 {
		approvalHistoryMenu.Disable()
	} else {
		approvalHistoryMenu.Enable()
	}
	for index, entries := range pages {
		if index == len(approvalPages) {
			approvalPages = append(approvalPages, approvalHistoryPage{menu: approvalHistoryMenu.AddSubMenuItem("", "")})
		}
		page := &approvalPages[index]
		first := index*approvalHistoryPageSize + 1
		dates := entries[0].Request.CreatedAt.Local().Format("02.01")
		if last := entries[len(entries)-1].Request.CreatedAt.Local().Format("02.01"); last != dates {
			dates += "–" + last
		}
		title := fmt.Sprintf("%d–%d · %s", first, first+len(entries)-1, dates)
		if index == 0 {
			title = i18n.T(lang, "approval.latest") + " · " + title
		}
		page.menu.SetTitle(title)
		page.menu.Show()
		for slot, entry := range entries {
			if slot == len(page.rows) {
				page.rows = append(page.rows, approvalReadOnlyRow{row: page.menu.AddSubMenuItem("", "")})
			}
			updateApprovalRow(&page.rows[slot], entry, true)
			page.rows[slot].row.Show()
		}
		for slot := len(entries); slot < len(page.rows); slot++ {
			page.rows[slot].row.Hide()
		}
	}
	for index := len(pages); index < len(approvalPages); index++ {
		approvalPages[index].menu.Hide()
	}
}

func updateApprovalRow(view *approvalReadOnlyRow, entry approval.Entry, history bool) {
	signature := entry.Request.ID + "|" + entry.Status.State + "|" + string(lang)
	if view.signature == signature {
		return
	}
	title := approvalPurpose(entry.Request, 58)
	if history {
		title = entry.Request.CreatedAt.Local().Format("15:04") + " · " +
			i18n.T(lang, "approval.state."+entry.Status.State) + " · " + approvalPurpose(entry.Request, 46)
	}
	view.row.SetTitle(title)
	view.row.SetTooltip(shortApprovalDetail(entry.Request, 800))
	lines := approvalDetailLines(entry, lang)
	for index, line := range lines {
		if index == len(view.details) {
			item := view.row.AddSubMenuItem("", "")
			item.Disable()
			view.details = append(view.details, item)
		}
		view.details[index].SetTitle(line)
		view.details[index].Show()
	}
	for index := len(lines); index < len(view.details); index++ {
		view.details[index].Hide()
	}
	view.signature = signature
}

func approvalPurpose(request approval.Request, limit int) string {
	text := strings.Join(strings.Fields(request.Purpose), " ")
	if text == "" {
		text = approvalKindTitle(lang, request.Kind)
	}
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return text
}

func approvalDetailLines(entry approval.Entry, activeLang i18n.Lang) []string {
	r := entry.Request
	scope := r.ActiveWing + " → " + i18n.T(activeLang, "approval.all_projects")
	resource := r.Query
	if r.Kind == approval.KindCredential {
		scope = r.SourceWing + " → " + r.TargetWing
		resource = r.Resource
	}
	fields := []string{
		i18n.T(activeLang, "approval.state."+entry.Status.State),
		i18n.T(activeLang, "approval.projects") + ": " + scope,
		i18n.T(activeLang, "approval.resource") + ": " + resource,
		i18n.T(activeLang, "approval.purpose") + ": " + r.Purpose,
		i18n.T(activeLang, "approval.created") + ": " + r.CreatedAt.Local().Format("02.01.2006 15:04"),
		"ID: " + r.ID,
	}
	var lines []string
	for _, field := range fields {
		runes := []rune(strings.NewReplacer("\r", " ", "\n", " ").Replace(field))
		for len(runes) > 70 {
			lines = append(lines, string(runes[:70]))
			runes = runes[70:]
		}
		lines = append(lines, string(runes))
	}
	return lines
}

func handleApprovalMenu(id string, item approvalMenuEntry) {
	for {
		var approved bool
		select {
		case <-item.approve.ClickedCh:
			approved = true
		case <-item.deny.ClickedCh:
			approved = false
		}
		mu.Lock()
		activeLang := lang
		mu.Unlock()
		decideApprovalByID(id, approved, activeLang)
	}
}

func approvalQueueSections(entries []approval.Entry) ([]approval.Entry, [][]approval.Entry) {
	pending := make([]approval.Entry, 0, len(entries))
	history := make([]approval.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Status.State == "pending" {
			pending = append(pending, entry)
		} else {
			history = append(history, entry)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].Request.CreatedAt.Equal(pending[j].Request.CreatedAt) {
			return pending[i].Request.ID < pending[j].Request.ID
		}
		return pending[i].Request.CreatedAt.Before(pending[j].Request.CreatedAt)
	})
	sort.Slice(history, func(i, j int) bool {
		if history[i].Request.CreatedAt.Equal(history[j].Request.CreatedAt) {
			return history[i].Request.ID < history[j].Request.ID
		}
		return history[i].Request.CreatedAt.After(history[j].Request.CreatedAt)
	})
	pages := make([][]approval.Entry, 0, (len(history)+approvalHistoryPageSize-1)/approvalHistoryPageSize)
	for index := 0; index < len(history); index += approvalHistoryPageSize {
		pages = append(pages, history[index:min(index+approvalHistoryPageSize, len(history))])
	}
	return pending, pages
}
