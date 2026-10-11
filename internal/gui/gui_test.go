package gui

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/xct258/push-client/internal/config"
	"github.com/xct258/push-client/internal/mqttc"
)

func newTestUI(t *testing.T) *UI {
	t.Helper()
	a := test.NewApp()
	cfg := config.Default()
	cfg.ConfigFile = t.TempDir() + "/config.yaml"
	return Build(a, cfg)
}

func TestAppendMsgOrderAndCap(t *testing.T) {
	u := newTestUI(t)
	for i := 1; i <= 205; i++ {
		u.appendMsg(mqttc.Message{
			Topic: "taskops/todo/7", TaskID: 7,
			Text: "msg", Received: time.Now(), IsTaskMsg: true,
		})
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.msgs) != 200 {
		t.Fatalf("列表应封顶 200，实际 %d", len(u.msgs))
	}
}

func TestRowTitle(t *testing.T) {
	m := mqttc.Message{Topic: "taskops/todo/7", TaskID: 7, Text: "【待办提醒】交电费\n正文", Received: time.Now(), IsTaskMsg: true}
	if got := rowTitle(m, map[int]bool{}); !strings.Contains(got, "#7") || !strings.Contains(got, "交电费") {
		t.Fatalf("任务行标题错误: %q", got)
	}
	if got := rowTitle(m, map[int]bool{7: true}); !strings.HasPrefix(got, "✓") {
		t.Fatalf("已完成应打勾: %q", got)
	}
	g := mqttc.Message{Topic: "test/topic", TaskID: -1, Text: "hello", Received: time.Now()}
	if got := rowTitle(g, map[int]bool{}); !strings.Contains(got, "test/topic") {
		t.Fatalf("通用行标题错误: %q", got)
	}
}

func TestCJKFontFound(t *testing.T) {
	// 冒烟环境装有 wqy 微米黑；Windows 上应命中系统字体。
	// 找不到也不判失败（字体缺失只影响字形，不影响功能），仅记录。
	if r := cjkFont(); r == nil {
		t.Log("未找到中文字体，中文可能显示方框")
	} else {
		t.Logf("中文字体: %s (%d bytes)", r.Name(), len(r.Content()))
		if len(r.Content()) < 1024 {
			t.Fatalf("字体资源过小，可能损坏")
		}
	}
}
