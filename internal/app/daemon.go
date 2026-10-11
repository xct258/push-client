package app

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/xct258/push-client/internal/api"
	"github.com/xct258/push-client/internal/config"
	"github.com/xct258/push-client/internal/mqttc"
	"github.com/xct258/push-client/internal/notify"
)

// RunDaemon 常驻模式：MQTT 订阅 + Toast 弹窗 + 断线重连后 REST 补拉 + 控制台交互回执。
// 控制台命令：done <id> | snooze <id> [分钟] | tasks | records | quit
func RunDaemon(cfg *config.Config) int {
	rest := api.New(cfg.ServerURL, cfg.PushToken)

	// 启动前先健康检查（失败只告警，不退出，MQTT 可能仍可用）
	if _, err := rest.Health(); err != nil {
		log.Printf("[warn] push-server 不可达 %s: %v（MQTT 可能仍可用）", cfg.ServerURL, err)
	} else {
		log.Printf("[ok] push-server %s 健康", cfg.ServerURL)
	}

	reconn := make(chan struct{}, 4)
	var mc *mqttc.Client
	mc = mqttc.New(mqttc.Option{
		Broker: cfg.MQTTBroker, Port: cfg.MQTTPort, ClientID: cfg.MQTTClientID,
		User: cfg.MQTTUser, Pass: cfg.MQTTPass, QoS: cfg.QoS,
		Topics: cfg.SubscribeTopics(), FeedbackTopic: cfg.FeedbackTopic,
		OnMessage:   func(m mqttc.Message) { handleDownlink(cfg, m) },
		OnReconnect: reconn,
	})

	if err := mc.Connect(15 * time.Second); err != nil {
		log.Printf("[fatal] MQTT 连接失败 %s: %v", cfg.BrokerAddr(), err)
		return 1
	}
	defer mc.Disconnect()

	log.Printf("[ok] 订阅 %v，反馈主题 %s", cfg.SubscribeTopics(), cfg.FeedbackTopic)
	log.Printf("命令: done <id> | snooze <id> [分钟] | tasks | records | quit")

	// 首次启动即补拉一次离线期间错过的 pending 任务
	go catchUp(cfg, rest, "启动补拉")

	// 重连信号 -> 补拉
	go func() {
		for range reconn {
			catchUp(cfg, rest, "重连补拉")
		}
	}()

	// 信号优雅退出
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println("\n收到退出信号，关闭中…")
		mc.Disconnect()
		os.Exit(0)
	}()

	// 控制台交互
	sc := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			fmt.Print("> ")
			continue
		}
		if handleConsole(cfg, rest, mc, line) {
			return 0
		}
		fmt.Print("> ")
	}
	return 0
}

// handleDownlink 下行处理：控制台打印 + 系统 Toast + 可选外部命令。
func handleDownlink(cfg *config.Config, m mqttc.Message) {
	ts := m.Received.Format("15:04:05")
	var title string
	if m.IsTaskMsg {
		title = fmt.Sprintf("【待办提醒】#%d", m.TaskID)
		fmt.Printf("\n[%s] %s\n%s\n  -> done %d | snooze %d [分钟]\n> ", ts, title, m.Text, m.TaskID, m.TaskID)
	} else {
		title = "MQTT 消息 " + m.Topic
		if m.TaskID > 0 {
			fmt.Printf("\n[%s] [%s] (id=%d)\n%s\n> ", ts, m.Topic, m.TaskID, m.Text)
		} else {
			fmt.Printf("\n[%s] [%s]\n%s\n> ", ts, m.Topic, m.Text)
		}
	}
	if !cfg.NoNotify {
		if err := notify.Notify(title, truncate(m.Text, 240)); err != nil {
			log.Printf("[warn] %v", err)
		}
	}
	if cfg.NotifyCmd != "" {
		go func() {
			cmd := exec.Command(cfg.NotifyCmd, title, m.Text, m.Topic)
			if out, err := cmd.CombinedOutput(); err != nil {
				log.Printf("[warn] notify-cmd 失败: %v %s", err, string(out))
			}
		}()
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// catchUp 离线补拉：GET /api/tasks?status=pending，展示 schedule_at 已过或未来24h 的任务。
func catchUp(cfg *config.Config, rest *api.Client, reason string) {
	resp, err := rest.Tasks(cfg.ServerName, "pending", 200)
	if err != nil {
		log.Printf("[补拉:%s] 查询失败: %v", reason, err)
		return
	}
	now := time.Now()
	shown := 0
	for _, t := range resp.Tasks {
		when, err := time.Parse(time.RFC3339, t.ScheduleAt)
		if err != nil {
			continue
		}
		missed := !when.After(now)
		soon := when.After(now) && when.Before(now.Add(24*time.Hour))
		if !missed && !soon {
			continue
		}
		tag := "将到"
		if missed {
			tag = "错过"
		}
		fmt.Printf("[补拉:%s][%s] #%d %s | 排期 %s\n  %s\n  -> done %d | snooze %d\n",
			reason, tag, t.TaskID, firstLine(t.Title, t.ClientRef), t.ScheduleAt, firstLine(t.Message, ""), t.TaskID, t.TaskID)
		if !cfg.NoNotify {
			_ = notify.Notify(fmt.Sprintf("【补拉·%s】#%d %s", tag, t.TaskID, t.Title), truncate(t.Message, 200))
		}
		shown++
	}
	log.Printf("[补拉:%s] pending=%d，本次展示=%d", reason, resp.Total, shown)
}

func firstLine(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(strings.SplitN(a, "\n", 2)[0])
	}
	return b
}

// handleConsole 控制台命令，返回 true 表示退出。
func handleConsole(cfg *config.Config, rest *api.Client, mc *mqttc.Client, line string) bool {
	f := strings.Fields(line)
	switch strings.ToLower(f[0]) {
	case "quit", "exit", "q":
		mc.Disconnect()
		return true
	case "done", "finish":
		if len(f) < 2 {
			fmt.Println("用法: done <task_id>")
			return false
		}
		id, err := strconv.Atoi(f[1])
		if err != nil {
			fmt.Println("task_id 须为数字")
			return false
		}
		Feedback(cfg, rest, mc, id, "done", 0)
	case "snooze", "delay":
		if len(f) < 2 {
			fmt.Println("用法: snooze <task_id> [分钟，默认60]")
			return false
		}
		id, err := strconv.Atoi(f[1])
		if err != nil {
			fmt.Println("task_id 须为数字")
			return false
		}
		min := 60
		if len(f) >= 3 {
			if m, err := strconv.Atoi(f[2]); err == nil && m > 0 {
				min = m
			}
		}
		Feedback(cfg, rest, mc, id, "snooze", min)
	case "tasks":
		resp, err := rest.Tasks(cfg.ServerName, "pending", 100)
		if err != nil {
			fmt.Println("查询失败:", err)
			return false
		}
		fmt.Printf("pending %d:\n", resp.Total)
		for _, t := range resp.Tasks {
			fmt.Printf("  #%d [%s] %s | %s | 排期 %s\n", t.TaskID, t.Status, t.Title, t.ClientRef, t.ScheduleAt)
		}
	case "records":
		resp, err := rest.Records("", "", 10, 0)
		if err != nil {
			fmt.Println("查询失败:", err)
			return false
		}
		fmt.Printf("共 %d 条，最新 10 条:\n", resp.Total)
		for _, r := range resp.Records {
			fmt.Printf("  #%d [%s] %s: %.60s\n", r.ID, r.ServerName, r.Topic, strings.ReplaceAll(r.Message, "\n", " "))
		}
	default:
		fmt.Println("未知命令，可用: done | snooze | tasks | records | quit")
	}
	return false
}

// Feedback 先走 MQTT 上行，失败自动降级 REST 备用通道。
func Feedback(cfg *config.Config, rest *api.Client, mc *mqttc.Client, taskID int, action string, minutes int) {
	if minutes <= 0 {
		minutes = 60
	}
	if mc != nil && mc.Connected() {
		if err := mc.Feedback(taskID, action, minutes); err != nil {
			fmt.Println("MQTT 反馈失败，转 REST 备用:", err)
		} else {
			fmt.Printf("已发送 %s #%d（MQTT %s）\n", action, taskID, cfg.FeedbackTopic)
			return
		}
	}
	res, err := rest.Feedback(taskID, action, minutes)
	if err != nil {
		fmt.Println("REST 反馈失败:", err)
		return
	}
	fmt.Printf("已发送 %s #%d（REST 备用）: %v\n", action, taskID, res["status"])
}
