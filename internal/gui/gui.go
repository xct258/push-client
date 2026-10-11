package gui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/xct258/push-client/internal/api"
	"github.com/xct258/push-client/internal/config"
	"github.com/xct258/push-client/internal/mqttc"
)

// UI 图形界面全部状态。网络回调一律经 fyne.Do 切回主线程再碰控件。
type UI struct {
	app fyne.App
	win fyne.Window
	cfg *config.Config

	rest   *api.Client
	reconn chan struct{}
	mu     sync.Mutex
	mc     *mqttc.Client

	// 提醒页
	msgs     []mqttc.Message
	doneIds  map[int]bool
	msgList  *widget.List
	selMsg   int
	msgTitle *widget.Label
	msgBody  *widget.Label
	msgMeta  *widget.Label
	btnDone  *widget.Button
	btnSnooz *widget.Button
	snoozeS  *widget.Select

	// 任务页
	tasks      []api.Task
	taskList   *widget.List
	selTask    int
	taskDetail *widget.Label
	tBtnDone   *widget.Button
	tBtnSnooz  *widget.Button
	tSnoozeS   *widget.Select

	// 记录页
	records      []api.Record
	recordList   *widget.List
	recordDetail *widget.Label

	// 设置页
	eServer, eToken, eMqtt, ePort, eBase, eFeedback, eServerName, eExtra *widget.Entry
	status                                                               *widget.Label
}

// Build 在给定 fyne.App 上组装界面（测试可用 test.NewApp 传入）。
func Build(a fyne.App, cfg *config.Config) *UI {
	u := &UI{
		app: a, cfg: cfg,
		rest: api.New(cfg.ServerURL, cfg.PushToken), reconn: make(chan struct{}, 4),
		doneIds: map[int]bool{}, selMsg: -1, selTask: -1,
	}
	u.win = a.NewWindow("MQTT 推送客户端")
	u.win.Resize(fyne.NewSize(880, 640))

	tabs := container.NewAppTabs(
		container.NewTabItem("提醒", u.msgTab()),
		container.NewTabItem("定时任务", u.taskTab()),
		container.NewTabItem("推送记录", u.recordTab()),
		container.NewTabItem("设置", u.settingTab()),
	)
	tabs.SetTabLocation(container.TabLocationTop)
	u.status = widget.NewLabel("未连接")
	u.win.SetContent(container.NewBorder(nil, u.status, nil, nil, tabs))

	u.setupTray()
	u.win.SetCloseIntercept(func() { u.win.Hide() })
	return u
}

// Run 启动 GUI（阻塞到窗口退出）。窗口先出现，MQTT 在后台连接，网络再慢也不白屏。
func Run(cfg *config.Config) int {
	fmt.Printf("push-client %s 图形界面启动中…\n  HTTP: %s\n  MQTT: %s\n  配置文件: %s\n  %s\n",
		config.Version, cfg.ServerURL, cfg.BrokerAddr(), cfg.ConfigFile, FontStatus())
	a := app.NewWithID("xct258.push-client")
	a.Settings().SetTheme(CJKTheme())
	u := Build(a, cfg)

	go u.consumeReconnect()
	go u.connectLoop()
	go u.fetchTasks(false)
	go u.fetchRecords()

	if smoke() {
		go func() {
			if text := os.Getenv("PUSH_CLIENT_GUI_SMOKE_MSG"); text != "" {
				time.Sleep(1 * time.Second)
				u.onMessage(mqttc.Message{
					Topic: "taskops/todo/7", TaskID: 7, Text: text,
					Received: time.Now(), IsTaskMsg: true,
				})
			}
			time.Sleep(4 * time.Second)
			fyne.Do(a.Quit)
		}()
	}
	u.win.ShowAndRun()
	return 0
}

func smoke() bool { return os.Getenv("PUSH_CLIENT_GUI_SMOKE") != "" }

// ---------------- 提醒页 ----------------

func (u *UI) msgTab() *fyne.Container {
	u.msgTitle = widget.NewLabel("暂无提醒")
	u.msgTitle.TextStyle.Bold = true
	u.msgMeta = widget.NewLabel("")
	u.msgBody = widget.NewLabel("")
	u.msgBody.Wrapping = fyne.TextWrapWord
	u.msgTitle.Wrapping = fyne.TextWrapWord

	u.snoozeS = widget.NewSelect([]string{"15", "30", "60", "120", "240"}, nil)
	u.snoozeS.SetSelected("60")
	u.btnDone = widget.NewButton("完成", func() { u.feedbackSelected("done") })
	u.btnSnooz = widget.NewButton("推迟", func() { u.feedbackSelected("snooze") })
	u.btnDone.Disable()
	u.btnSnooz.Disable()

	u.msgList = widget.NewList(
		func() int { u.mu.Lock(); defer u.mu.Unlock(); return len(u.msgs) },
		func() fyne.CanvasObject {
			t := widget.NewLabel("")
			t.TextStyle.Bold = true
			t.Truncation = fyne.TextTruncateEllipsis
			p := widget.NewLabel("")
			p.Truncation = fyne.TextTruncateEllipsis
			return container.NewVBox(t, p)
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			u.mu.Lock()
			defer u.mu.Unlock()
			if int(id) >= len(u.msgs) {
				return
			}
			m := u.msgs[id]
			box := o.(*fyne.Container)
			box.Objects[0].(*widget.Label).SetText(rowTitle(m, u.doneIds))
			box.Objects[1].(*widget.Label).SetText(rowPreview(m))
		},
	)
	u.msgList.OnSelected = func(id widget.ListItemID) { u.showMsg(int(id)) }
	u.msgList.OnUnselected = func(widget.ListItemID) { u.selMsg = -1 }

	detail := container.NewVBox(
		u.msgTitle, u.msgMeta,
		container.NewVScroll(u.msgBody),
		container.NewHBox(u.btnDone, u.snoozeS, u.btnSnooz),
	)
	split := container.NewHSplit(u.msgList, detail)
	split.Offset = 0.42
	return container.NewBorder(
		widget.NewLabel("收到提醒会弹窗并出现在这里，选中查看详情并完成/推迟"),
		nil, nil, nil, split,
	)
}

func rowTitle(m mqttc.Message, done map[int]bool) string {
	var t string
	if m.IsTaskMsg {
		t = fmt.Sprintf("#%d %s", m.TaskID, firstLine(m.Text, ""))
		if done[m.TaskID] {
			t = "✓ " + t
		}
	} else {
		t = fmt.Sprintf("[%s] %s", m.Topic, firstLine(m.Text, ""))
	}
	if t == "" || strings.HasPrefix(t, "#") && strings.TrimSpace(firstLine(m.Text, "")) == "" {
		t = fmt.Sprintf("#%d", m.TaskID)
	}
	return fmt.Sprintf("%s · %s", t, m.Received.Format("15:04"))
}

func rowPreview(m mqttc.Message) string {
	lines := strings.SplitN(strings.TrimSpace(m.Text), "\n", 2)
	if len(lines) == 2 {
		return firstLine(lines[1], "")
	}
	return ""
}

func firstLine(s, def string) string {
	if t := strings.TrimSpace(strings.SplitN(s, "\n", 2)[0]); t != "" {
		return t
	}
	return def
}

func (u *UI) showMsg(idx int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if idx < 0 || idx >= len(u.msgs) {
		return
	}
	u.selMsg = idx
	m := u.msgs[idx]
	if m.IsTaskMsg {
		u.msgTitle.SetText(fmt.Sprintf("【待办提醒】#%d", m.TaskID))
	} else {
		u.msgTitle.SetText("MQTT 消息 " + m.Topic)
	}
	u.msgMeta.SetText(fmt.Sprintf("%s · %s", m.Topic, m.Received.Format("2006-01-02 15:04:05")))
	u.msgBody.SetText(m.Text)
	if m.IsTaskMsg && m.TaskID > 0 && !u.doneIds[m.TaskID] {
		u.btnDone.Enable()
		u.btnSnooz.Enable()
	} else {
		u.btnDone.Disable()
		u.btnSnooz.Disable()
	}
}

func (u *UI) feedbackSelected(action string) {
	u.mu.Lock()
	if u.selMsg < 0 || u.selMsg >= len(u.msgs) {
		u.mu.Unlock()
		return
	}
	m := u.msgs[u.selMsg]
	u.mu.Unlock()
	if !m.IsTaskMsg || m.TaskID <= 0 {
		return
	}
	min := 60
	if action == "snooze" {
		if n, err := strconv.Atoi(u.snoozeS.Selected); err == nil && n > 0 {
			min = n
		}
	}
	u.doFeedback(m.TaskID, action, min, func(ok bool) {
		if ok && action == "done" {
			u.mu.Lock()
			u.doneIds[m.TaskID] = true
			u.mu.Unlock()
		}
	})
}

// appendMsg 纯状态变更（无 UI 操作），后台 goroutine 与测试可直接调。
func (u *UI) appendMsg(m mqttc.Message) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.msgs = append([]mqttc.Message{m}, u.msgs...)
	if len(u.msgs) > 200 {
		u.msgs = u.msgs[:200]
	}
}

// onMessage MQTT 下行入口：记列表 + 系统通知 + 主线程刷新。
func (u *UI) onMessage(m mqttc.Message) {
	u.appendMsg(m)
	title := "MQTT 消息 " + m.Topic
	if m.IsTaskMsg {
		title = fmt.Sprintf("【待办提醒】#%d", m.TaskID)
	}
	u.app.SendNotification(fyne.NewNotification(title, firstLine(m.Text, m.Topic)))
	fyne.Do(func() {
		u.msgList.Refresh()
		u.mu.Lock()
		empty := u.selMsg == -1
		u.mu.Unlock()
		if empty {
			u.msgList.Select(0)
		}
	})
}

// ---------------- 定时任务页 ----------------

func (u *UI) taskTab() *fyne.Container {
	u.taskDetail = widget.NewLabel("选中任务查看详情")
	u.taskDetail.Wrapping = fyne.TextWrapWord
	u.tSnoozeS = widget.NewSelect([]string{"15", "30", "60", "120", "240"}, nil)
	u.tSnoozeS.SetSelected("60")
	u.tBtnDone = widget.NewButton("完成", func() { u.feedbackTask("done") })
	u.tBtnSnooz = widget.NewButton("推迟", func() { u.feedbackTask("snooze") })
	u.tBtnDone.Disable()
	u.tBtnSnooz.Disable()

	u.taskList = widget.NewList(
		func() int { u.mu.Lock(); defer u.mu.Unlock(); return len(u.tasks) },
		func() fyne.CanvasObject {
			t := widget.NewLabel("")
			t.TextStyle.Bold = true
			t.Truncation = fyne.TextTruncateEllipsis
			s := widget.NewLabel("")
			s.Truncation = fyne.TextTruncateEllipsis
			return container.NewVBox(t, s)
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			u.mu.Lock()
			defer u.mu.Unlock()
			if int(id) >= len(u.tasks) {
				return
			}
			t := u.tasks[id]
			box := o.(*fyne.Container)
			box.Objects[0].(*widget.Label).SetText(fmt.Sprintf("#%d [%s] %s", t.TaskID, t.Status, firstLine(t.Title, t.ClientRef)))
			box.Objects[1].(*widget.Label).SetText(fmt.Sprintf("排期 %s · %s", t.ScheduleAt, firstLine(t.Message, "")))
		},
	)
	u.taskList.OnSelected = func(id widget.ListItemID) { u.showTask(int(id)) }
	u.taskList.OnUnselected = func(widget.ListItemID) { u.selTask = -1 }

	refresh := widget.NewButton("刷新", func() { go u.fetchTasks(true) })
	detail := container.NewVBox(
		container.NewVScroll(u.taskDetail),
		container.NewHBox(u.tBtnDone, u.tSnoozeS, u.tBtnSnooz),
	)
	split := container.NewHSplit(u.taskList, detail)
	split.Offset = 0.5
	return container.NewBorder(refresh, nil, nil, nil, split)
}

func (u *UI) showTask(idx int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if idx < 0 || idx >= len(u.tasks) {
		return
	}
	u.selTask = idx
	t := u.tasks[idx]
	u.taskDetail.SetText(fmt.Sprintf("#%d [%s]\n标题：%s\n标识：%s\n主题：%s\n排期：%s\n回调：%s\n\n%s",
		t.TaskID, t.Status, t.Title, t.ClientRef, t.Topic, t.ScheduleAt, t.Callback, t.Message))
	if t.Status == "pending" || t.Status == "sent" {
		u.tBtnDone.Enable()
		u.tBtnSnooz.Enable()
	} else {
		u.tBtnDone.Disable()
		u.tBtnSnooz.Disable()
	}
}

func (u *UI) feedbackTask(action string) {
	u.mu.Lock()
	if u.selTask < 0 || u.selTask >= len(u.tasks) {
		u.mu.Unlock()
		return
	}
	id := u.tasks[u.selTask].TaskID
	u.mu.Unlock()
	min := 60
	if action == "snooze" {
		if n, err := strconv.Atoi(u.tSnoozeS.Selected); err == nil && n > 0 {
			min = n
		}
	}
	u.doFeedback(id, action, min, func(ok bool) {
		if ok {
			go u.fetchTasks(false)
		}
	})
}

func (u *UI) fetchTasks(manual bool) {
	resp, err := u.rest.Tasks(u.cfg.ServerName, "", 200)
	if err != nil {
		if manual {
			fyne.Do(func() { dialog.ShowError(err, u.win) })
		}
		return
	}
	u.mu.Lock()
	u.tasks = resp.Tasks
	u.mu.Unlock()
	fyne.Do(func() { u.taskList.Refresh() })
}

// ---------------- 推送记录页 ----------------

func (u *UI) recordTab() *fyne.Container {
	u.recordDetail = widget.NewLabel("选中记录查看正文")
	u.recordDetail.Wrapping = fyne.TextWrapWord
	u.recordList = widget.NewList(
		func() int { u.mu.Lock(); defer u.mu.Unlock(); return len(u.records) },
		func() fyne.CanvasObject {
			t := widget.NewLabel("")
			t.TextStyle.Bold = true
			t.Truncation = fyne.TextTruncateEllipsis
			s := widget.NewLabel("")
			s.Truncation = fyne.TextTruncateEllipsis
			return container.NewVBox(t, s)
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			u.mu.Lock()
			defer u.mu.Unlock()
			if int(id) >= len(u.records) {
				return
			}
			r := u.records[id]
			box := o.(*fyne.Container)
			box.Objects[0].(*widget.Label).SetText(fmt.Sprintf("#%d [%s] %s", r.ID, r.ServerName, r.Topic))
			box.Objects[1].(*widget.Label).SetText(fmt.Sprintf("%s · %s", r.CreatedAt, firstLine(r.Message, "")))
		},
	)
	u.recordList.OnSelected = func(id widget.ListItemID) {
		u.mu.Lock()
		defer u.mu.Unlock()
		if int(id) < len(u.records) {
			u.recordDetail.SetText(u.records[id].Message)
		}
	}
	refresh := widget.NewButton("刷新", func() { go u.fetchRecords() })
	split := container.NewHSplit(u.recordList, container.NewVScroll(u.recordDetail))
	split.Offset = 0.45
	return container.NewBorder(refresh, nil, nil, nil, split)
}

func (u *UI) fetchRecords() {
	resp, err := u.rest.Records("", "", 100, 0)
	if err != nil {
		fyne.Do(func() { dialog.ShowError(err, u.win) })
		return
	}
	u.mu.Lock()
	u.records = resp.Records
	u.mu.Unlock()
	fyne.Do(func() { u.recordList.Refresh() })
}

// ---------------- 设置页 ----------------

func (u *UI) settingTab() *fyne.Container {
	c := u.cfg
	u.eServer = widget.NewEntry()
	u.eServer.SetText(c.ServerURL)
	u.eToken = widget.NewPasswordEntry()
	u.eToken.SetText(c.PushToken)
	u.eMqtt = widget.NewEntry()
	u.eMqtt.SetText(c.MQTTBroker)
	u.ePort = widget.NewEntry()
	u.ePort.SetText(strconv.Itoa(c.MQTTPort))
	u.eBase = widget.NewEntry()
	u.eBase.SetText(c.BaseTopic)
	u.eFeedback = widget.NewEntry()
	u.eFeedback.SetText(c.FeedbackTopic)
	u.eServerName = widget.NewEntry()
	u.eServerName.SetText(c.ServerName)
	u.eExtra = widget.NewEntry()
	u.eExtra.SetText(strings.Join(c.ExtraTopics, ","))
	u.eExtra.SetPlaceHolder("test/topic, notice/#（逗号分隔，可空）")

	form := widget.NewForm(
		widget.NewFormItem("push-server 地址", u.eServer),
		widget.NewFormItem("X-Push-Token", u.eToken),
		widget.NewFormItem("MQTT 地址", u.eMqtt),
		widget.NewFormItem("MQTT 端口", u.ePort),
		widget.NewFormItem("下行基主题", u.eBase),
		widget.NewFormItem("上行反馈主题", u.eFeedback),
		widget.NewFormItem("补拉 server_name", u.eServerName),
		widget.NewFormItem("附加订阅", u.eExtra),
	)
	save := widget.NewButton("保存并重连", func() { u.saveSettings() })
	test := widget.NewButton("测试 HTTP 连接", func() {
		go func() {
			if _, err := api.New(strings.TrimRight(strings.TrimSpace(u.eServer.Text), "/"), u.cfg.PushToken).Health(); err != nil {
				fyne.Do(func() { dialog.ShowError(err, u.win) })
				return
			}
			fyne.Do(func() { dialog.ShowInformation("连接成功", "push-server HTTP 正常", u.win) })
		}()
	})
	font := widget.NewLabel(FontStatus())
	return container.NewVBox(form, container.NewHBox(save, test), font)
}

func (u *UI) saveSettings() {
	c := u.cfg
	c.ServerURL = strings.TrimRight(strings.TrimSpace(u.eServer.Text), "/")
	c.PushToken = strings.TrimSpace(u.eToken.Text)
	c.MQTTBroker = strings.TrimSpace(u.eMqtt.Text)
	if p, err := strconv.Atoi(strings.TrimSpace(u.ePort.Text)); err == nil && p > 0 && p <= 65535 {
		c.MQTTPort = p
	} else {
		fyne.Do(func() { dialog.ShowError(fmt.Errorf("端口须为 1-65535"), u.win) })
		return
	}
	c.BaseTopic = strings.Trim(strings.TrimSpace(u.eBase.Text), "/")
	c.FeedbackTopic = strings.Trim(strings.TrimSpace(u.eFeedback.Text), "/")
	c.ServerName = strings.TrimSpace(u.eServerName.Text)
	c.ExtraTopics = nil
	for _, t := range strings.Split(u.eExtra.Text, ",") {
		if t = strings.TrimSpace(t); t != "" {
			c.ExtraTopics = append(c.ExtraTopics, t)
		}
	}
	u.rest.BaseURL = c.ServerURL
	u.rest.Token = c.PushToken
	if err := config.Save(c.ConfigFile, c); err != nil {
		dialog.ShowError(err, u.win)
		return
	}
	dialog.ShowInformation("已保存", "配置已保存，正在重连 MQTT…", u.win)
	go u.reconnect()
}

// ---------------- 连接管理 ----------------

func (u *UI) setStatus(s string) {
	fyne.Do(func() { u.status.SetText(s) })
}

func (u *UI) connectLoop() {
	for {
		mc := u.newClient()
		if err := mc.Connect(15 * time.Second); err != nil {
			u.setStatus(fmt.Sprintf("MQTT 连接失败（%v），5 秒后重试…（可在设置页修改地址）", shortErr(err)))
			time.Sleep(5 * time.Second)
			continue
		}
		u.setMc(mc)
		u.setStatus(fmt.Sprintf("已连接 %s · 订阅 %v", u.cfg.BrokerAddr(), u.cfg.SubscribeTopics()))
		return
	}
}

func (u *UI) reconnect() {
	u.closeMc()
	u.setStatus("重连中…")
	go u.connectLoop()
}

func (u *UI) newClient() *mqttc.Client {
	c := u.cfg
	return mqttc.New(mqttc.Option{
		Broker: c.MQTTBroker, Port: c.MQTTPort, ClientID: c.MQTTClientID,
		User: c.MQTTUser, Pass: c.MQTTPass, QoS: c.QoS,
		Topics: c.SubscribeTopics(), FeedbackTopic: c.FeedbackTopic,
		OnMessage:   u.onMessage,
		OnReconnect: u.reconn,
	})
}

func (u *UI) setMc(mc *mqttc.Client) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.mc = mc
}

func (u *UI) closeMc() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.mc != nil {
		u.mc.Disconnect()
		u.mc = nil
	}
}

func (u *UI) consumeReconnect() {
	for range u.reconn {
		u.catchUp()
		u.setStatus(fmt.Sprintf("已连接 %s · 订阅 %v", u.cfg.BrokerAddr(), u.cfg.SubscribeTopics()))
	}
}

// catchUp 离线补拉：pending 中已过或 24h 内到期的中转为提醒条目进列表。
func (u *UI) catchUp() {
	resp, err := u.rest.Tasks(u.cfg.ServerName, "pending", 200)
	if err != nil {
		return
	}
	now := time.Now()
	n := 0
	for _, t := range resp.Tasks {
		when, err := time.Parse(time.RFC3339, t.ScheduleAt)
		if err != nil {
			continue
		}
		if when.After(now.Add(24 * time.Hour)) {
			continue
		}
		title := strings.TrimSpace(t.Title)
		body := strings.TrimSpace(t.Message)
		text := body
		if title != "" {
			text = "【待办提醒】" + title
			if body != "" {
				text += "\n" + body
			}
		} else if body == "" {
			text = t.ClientRef
		}
		u.appendMsg(mqttc.Message{
			Topic:  strings.Trim(t.Topic, "/") + "/" + strconv.Itoa(t.TaskID),
			TaskID: t.TaskID, Text: text, Received: time.Now(), IsTaskMsg: true,
		})
		n++
	}
	if n > 0 {
		fyne.Do(func() { u.msgList.Refresh() })
		u.app.SendNotification(fyne.NewNotification("补拉", fmt.Sprintf("有 %d 条待办需关注", n)))
	}
}

// doFeedback 先走 MQTT 上行，失败降级 REST；结果弹对话框，成功回调跑主线程。
func (u *UI) doFeedback(taskID int, action string, minutes int, after func(ok bool)) {
	if minutes <= 0 {
		minutes = 60
	}
	go func() {
		u.mu.Lock()
		mc := u.mc
		u.mu.Unlock()
		if mc != nil && mc.Connected() {
			if err := mc.Feedback(taskID, action, minutes); err != nil {
				fyne.Do(func() { dialog.ShowError(err, u.win) })
			} else {
				fyne.Do(func() {
					dialog.ShowInformation("已发送", fmt.Sprintf("%s #%d 已发送", actionName(action), taskID), u.win)
					after(true)
					u.msgList.Refresh()
				})
				return
			}
		}
		res, err := u.rest.Feedback(taskID, action, minutes)
		if err != nil {
			fyne.Do(func() { dialog.ShowError(fmt.Errorf("REST 备用也失败: %w", err), u.win) })
			return
		}
		fyne.Do(func() {
			dialog.ShowInformation("已发送", fmt.Sprintf("%s #%d（备用通道 %v）", actionName(action), taskID, res["status"]), u.win)
			after(true)
			u.msgList.Refresh()
		})
	}()
}

func actionName(a string) string {
	if a == "done" {
		return "完成"
	}
	return "推迟"
}

func shortErr(err error) string {
	s := err.Error()
	if len([]rune(s)) > 80 {
		return string([]rune(s)[:80]) + "…"
	}
	return s
}

// ---------------- 托盘 ----------------

func (u *UI) setupTray() {
	desk, ok := u.app.(desktop.App)
	if !ok {
		return
	}
	show := fyne.NewMenuItem("显示窗口", func() { u.win.Show() })
	quit := fyne.NewMenuItem("退出", func() { u.app.Quit() })
	desk.SetSystemTrayMenu(fyne.NewMenu("推送客户端", show, quit))
	desk.SetSystemTrayIcon(theme.InfoIcon())
}
