package main

import (
	"ariadne/internal/approval"
	"ariadne/internal/i18n"
	"fmt"

	"fyne.io/systray"
)

type approvalMenuEntry struct {
	row, approve, deny *systray.MenuItem
}

var (
	approvalQueueMenu  *systray.MenuItem
	approvalQueueItems = make(map[string]approvalMenuEntry)
)

// Called with mu held. Actions retain an exact request ID, never a moving head.
func refreshApprovalQueueLocked(entries []approval.Entry, err error) {
	if approvalQueueMenu == nil {
		return
	}
	approvalQueueMenu.SetTitle(i18n.T(lang, "approval.queue"))
	if err != nil {
		approvalQueueMenu.SetTitle(i18n.T(lang, "approval.queue") + ": " + i18n.T(lang, "notify.failed"))
		for _, item := range approvalQueueItems {
			item.approve.Disable()
			item.deny.Disable()
		}
		return
	}
	for _, entry := range entries {
		id := entry.Request.ID
		item, exists := approvalQueueItems[id]
		if !exists {
			item.row = approvalQueueMenu.AddSubMenuItem("", "")
			item.approve = item.row.AddSubMenuItem("", "")
			item.deny = item.row.AddSubMenuItem("", "")
			approvalQueueItems[id] = item
			go handleApprovalMenu(id, item)
		}
		item.row.SetTitle(fmt.Sprintf("%s · %s · %s · %s",
			entry.Request.CreatedAt.Local().Format("01-02 15:04"), id[:12],
			i18n.T(lang, "approval.state."+entry.Status.State), shortApprovalDetail(entry.Request, 96)))
		item.row.SetTooltip(id + " · " + shortApprovalDetail(entry.Request, 800))
		item.approve.SetTitle(i18n.T(lang, "menu.approve"))
		item.deny.SetTitle(i18n.T(lang, "menu.deny"))
		if entry.Status.State == "pending" {
			item.approve.Enable()
			item.deny.Enable()
		} else {
			item.approve.Disable()
			item.deny.Disable()
		}
	}
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
