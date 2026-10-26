package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// TestPickDoesNotBlockTheClick covers the rule the two buttons broke: a file
// dialog answers only when the user has answered it, so it must be asked from a
// goroutine of its own. Asked from the click itself, the click never returns,
// the panel is never shown and the page looks like it ignored the click.
func TestPickDoesNotBlockTheClick(t *testing.T) {
	a := newApp(defaultSettings())
	asked := make(chan mygo.OpenDialogOptions, 1)
	release := make(chan struct{})
	answered := make(chan string, 1)
	defer func() { askDialog = mygo.Dialog.Open }()
	askDialog = func(opts mygo.OpenDialogOptions) ([]string, error) {
		asked <- opts
		<-release
		return []string{"/tmp/产品文档"}, nil
	}

	returned := make(chan struct{})
	go func() {
		a.pickFolder(func(dir string) { answered <- dir })
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("点击没有返回：拖住了事件处理")
	}
	select {
	case opts := <-asked:
		if !opts.Directory {
			t.Error("导入目录的对话框应当只能选目录")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("没有去问系统：文件选择窗口没有打开")
	}
	select {
	case dir := <-answered:
		t.Fatalf("对话框还没回答就把 %q 交了出去", dir)
	default:
	}
	close(release)
	select {
	case dir := <-answered:
		if dir != "/tmp/产品文档" {
			t.Errorf("选中的目录 = %q；want /tmp/产品文档", dir)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("选了目录之后没有回到界面")
	}
}

// TestPickReportsAFailedDialog covers a dialog that cannot open: it says so
// rather than leaving the click without an answer.
func TestPickReportsAFailedDialog(t *testing.T) {
	a := newApp(defaultSettings())
	defer func() { askDialog = mygo.Dialog.Open }()
	askDialog = func(mygo.OpenDialogOptions) ([]string, error) { return nil, errNoDialog }
	a.pickFile(a.startAnalyze)
	for i := 0; i < 100 && a.flash == ""; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if a.flash == "" {
		t.Fatal("对话框打不开时没有任何提示")
	}
}

// errNoDialog stands for the system refusing to show a dialog.
var errNoDialog = errFailedDialog{}

type errFailedDialog struct{}

func (errFailedDialog) Error() string { return "测试：对话框没有打开" }
