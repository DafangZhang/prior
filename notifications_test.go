package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestNotificationCountsCountUnread covers what the sidebar's badge and the
// page's figures say: the badge counts what has not been read, not how long the
// list is, and the figures read unread first. The list itself keeps every
// notification, with its read state on the row.
func TestNotificationCountsCountUnread(t *testing.T) {
	notes := []note{
		{ID: 1, Title: "分析完成：春节活动需求文档", Time: "2025-01-06 10:00", ReportID: "1"},
		{ID: 2, Title: "分析降级：软件列表、软件详情需求文档", Time: "2023-10-26 09:00", ReportID: "2"},
		{ID: 3, Title: "分析降级：专区详情、素材详情需求文档", Time: "2023-10-30 09:00", ReportID: "3"},
		{ID: 4, Title: "分析完成：课程列表筛选项联动", Time: "2023-10-27 09:00", Read: true, ReportID: "4"},
		{ID: 5, Title: "分析完成：春节活动需求文档", Time: "2025-01-02 09:00", Read: true, ReportID: "5"},
	}
	a := newApp(defaultSettings())
	a.notes = notes
	if got := a.unreadNotes(); got != 3 {
		t.Fatalf("未读 %d 条；want 3（共 5 条）", got)
	}
	a.notes = nil
	if got := a.unreadNotes(); got != 0 {
		t.Errorf("没有通知时未读 = %d；want 0", got)
	}

	a.notes = notes
	tester := ui.NewTester(a.view, 1024, 680)
	tester.Frame()
	if err := tester.Click("通知"); err != nil {
		t.Fatalf("点击通知：%v", err)
	}
	for _, want := range []string{"3/5", "未读 3", "通知 5"} {
		if !tester.HasText(want) {
			t.Errorf("通知页没有显示 %q；界面文字：%v", want, tester.Texts())
		}
	}
	// Marking one notification read leaves the count of unread ones one
	// lower, and the badge follows.
	a.markNote(1, true)
	if got := a.unreadNotes(); got != 2 {
		t.Errorf("标记已读后未读 = %d；want 2", got)
	}
}
