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
	"sync"
	"syscall"
	"time"

	"github.com/xct258/push-client/internal/api"
	"github.com/xct258/push-client/internal/config"
	"github.com/xct258/push-client/internal/mqttc"
	"github.com/xct258/push-client/internal/notify"
)

// Version 客户端版本（启动横幅与 version 命令共用，确认 Windows 上跑的是不是最新构建）。
const Version = "v0.3.0"

// Daemon 常驻实例：持有可替换的 MQTT 客户端，支持运行时 set 改配置并重连。
type Daemon struct {
	cfg    *config.Config
	rest   *api.Client
	reconn chan struct{}
	mu     sync.Mutex
	mc     *mqttc.Client
}

// RunDaemon 常驻模式：MQTT 订阅 + Toast 弹窗 + 断线重连后 REST 补拉 + 控制台交互。
// 控制台命令：done <id> | snooze <id> [分钟] | tasks | records | set/get | quit
func RunDaemon(cfg *config.Config) int {
	d := &Daemon{cfg: cfg, rest: api.New(cfg.ServerURL, cfg.PushToken), reconn: make(chan struct{}, 4)}
	return d.run()
}

func (d *Daemon) run() int {
	cfg := d.cfg
	fmt.Printf("push-client %s 启动中…\n  HTTP: %s\n  MQTT: %s\n  订阅: %v\n  反馈: %s\n  配置文件: %s\n",
		Version, cfg.ServerURL, cfg.BrokerAddr(), cfg.SubscribeTopics(), cfg.FeedbackTopic, cfg.ConfigFile)

	// 首次运行（无配置文件）或 -setup：进软件内交互配置向导
	if !cfg.NoSetup && (cfg.ForceSetup || !cfg.ConfigFound) {
		SetupWizard(cfg)
		d.rest.BaseURL = cfg.ServerURL
		d.rest.Token = cfg.PushToken
		fmt.Printf("当前 MQTT: %s，订阅: %v\n", cfg.BrokerAddr(), cfg.SubscribeTopics())
	}

	// 启动前先健康检查（失败只告警，不退出，MQTT 可能仍可用）
	if _, err := d.rest.Health(); err != nil {
		log.Printf("[warn] push-server 不可达 %s: %v（MQTT 可能仍可用）", cfg.ServerURL, err)
	} else {
		log.Printf("[ok] push-server %s 健康", cfg.ServerURL)
	}

	// 连接失败无限重试：Windows 双击运行时直接退出会导致窗口一闪而过，
	// 保持重试可让用户看到错误并用 set 命令修正配置。
	d.connectForeground()
	defer d.closeMc()

	log.Printf("[ok] 订阅 %v，反馈主题 %s", cfg.SubscribeTopics(), cfg.FeedbackTopic)
	log.Printf("命令: done <id> | snooze <id> [分钟] | tasks | records | set/get | quit")

	// 首次启动即补拉一次离线期间错过的 pending 任务
	go catchUp(cfg, d.rest, "启动补拉")

	// 重连信号 -> 补拉
	go func() {
		for range d.reconn {
			catchUp(d.cfg, d.rest, "重连补拉")
		}
	}()

	// 信号优雅退出
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println("\n收到退出信号，关闭中…")
		d.closeMc()
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
		if d.handleConsole(line) {
			return 0
		}
		fmt.Print("> ")
	}
	return 0
}

func (d *Daemon) setMc(mc *mqttc.Client) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.mc = mc
}

func (d *Daemon) currentMc() *mqttc.Client {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.mc
}

func (d *Daemon) closeMc() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.mc != nil {
		d.mc.Disconnect()
		d.mc = nil
	}
}

// connectForeground 前台阻塞直到连上（供启动时调用）。
func (d *Daemon) connectForeground() {
	for {
		mc := d.newClient()
		if err := mc.Connect(15 * time.Second); err != nil {
			log.Printf("[fatal] MQTT 连接失败 %s: %v，5 秒后重试（可用 set mqtt <地址> 修改后自动重连）",
				d.cfg.BrokerAddr(), err)
			time.Sleep(5 * time.Second)
			continue
		}
		d.setMc(mc)
		return
	}
}

// reconnect 后台重连（供 set 改配置后调用，不阻塞控制台）。
func (d *Daemon) reconnect() {
	d.closeMc()
	go func() {
		for {
			mc := d.newClient()
			if err := mc.Connect(15 * time.Second); err != nil {
				log.Printf("[warn] MQTT 重连失败 %s: %v，5 秒后重试", d.cfg.BrokerAddr(), err)
				time.Sleep(5 * time.Second)
				continue
			}
			d.setMc(mc)
			log.Printf("[ok] 已重连 %s，订阅 %v", d.cfg.BrokerAddr(), d.cfg.SubscribeTopics())
			return
		}
	}()
}

func (d *Daemon) newClient() *mqttc.Client {
	cfg := d.cfg
	return mqttc.New(mqttc.Option{
		Broker: cfg.MQTTBroker, Port: cfg.MQTTPort, ClientID: cfg.MQTTClientID,
		User: cfg.MQTTUser, Pass: cfg.MQTTPass, QoS: cfg.QoS,
		Topics: cfg.SubscribeTopics(), FeedbackTopic: cfg.FeedbackTopic,
		OnMessage:   func(m mqttc.Message) { handleDownlink(cfg, m) },
		OnReconnect: d.reconn,
	})
}

// persist 保存当前配置到文件（set 命令每次修改后调用）。
func (d *Daemon) persist() {
	if err := config.Save(d.cfg.ConfigFile, d.cfg); err != nil {
		fmt.Println("保存配置失败:", err)
		return
	}
	fmt.Println("已保存到", d.cfg.ConfigFile)
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
func (d *Daemon) handleConsole(line string) bool {
	cfg := d.cfg
	f := strings.Fields(line)
	switch strings.ToLower(f[0]) {
	case "quit", "exit", "q":
		d.closeMc()
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
		Feedback(cfg, d.rest, d.currentMc(), id, "done", 0)
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
		Feedback(cfg, d.rest, d.currentMc(), id, "snooze", min)
	case "tasks":
		resp, err := d.rest.Tasks(cfg.ServerName, "pending", 100)
		if err != nil {
			fmt.Println("查询失败:", err)
			return false
		}
		fmt.Printf("pending %d:\n", resp.Total)
		for _, t := range resp.Tasks {
			fmt.Printf("  #%d [%s] %s | %s | 排期 %s\n", t.TaskID, t.Status, t.Title, t.ClientRef, t.ScheduleAt)
		}
	case "records":
		resp, err := d.rest.Records("", "", 10, 0)
		if err != nil {
			fmt.Println("查询失败:", err)
			return false
		}
		fmt.Printf("共 %d 条，最新 10 条:\n", resp.Total)
		for _, r := range resp.Records {
			fmt.Printf("  #%d [%s] %s: %.60s\n", r.ID, r.ServerName, r.Topic, strings.ReplaceAll(r.Message, "\n", " "))
		}
	case "get", "config", "show":
		printConfig(cfg)
	case "set":
		d.handleSet(f[1:])
	default:
		fmt.Println("未知命令，可用: done | snooze | tasks | records | get | set | quit")
	}
	return false
}

// handleSet 软件内改配置并保存，改 MQTT 相关项后自动重连。
// 用法: set mqtt <地址> | set mqtt-port <端口> | set server <URL> | set base <主题> |
//
//	set feedback <主题> | set server-name <名> | set extra <t1,t2> | set token <T>
func (d *Daemon) handleSet(args []string) {
	cfg := d.cfg
	if len(args) < 1 {
		fmt.Println("用法: set mqtt <地址> | set mqtt-port <端口> | set server <URL> | set base <主题> | set feedback <主题> | set server-name <名> | set extra <t1,t2> | set token <T>")
		return
	}
	needReconnect := false
	key := strings.ToLower(args[0])
	val := strings.Join(args[1:], " ")
	val = strings.TrimSpace(val)
	switch key {
	case "mqtt", "broker":
		if val == "" {
			fmt.Println("用法: set mqtt <地址>")
			return
		}
		cfg.MQTTBroker = val
		needReconnect = true
	case "mqtt-port", "port":
		p, err := strconv.Atoi(val)
		if err != nil || p <= 0 || p > 65535 {
			fmt.Println("端口须为 1-65535")
			return
		}
		cfg.MQTTPort = p
		needReconnect = true
	case "server", "url":
		if val == "" {
			fmt.Println("用法: set server <URL>")
			return
		}
		cfg.ServerURL = strings.TrimRight(val, "/")
		d.rest.BaseURL = cfg.ServerURL
	case "token":
		cfg.PushToken = val
		d.rest.Token = val
	case "base":
		if val == "" {
			fmt.Println("用法: set base <主题>")
			return
		}
		cfg.BaseTopic = strings.Trim(val, "/")
		needReconnect = true
	case "feedback":
		if val == "" {
			fmt.Println("用法: set feedback <主题>")
			return
		}
		cfg.FeedbackTopic = strings.Trim(val, "/")
	case "server-name":
		cfg.ServerName = val
	case "extra":
		cfg.ExtraTopics = nil
		for _, t := range strings.Split(val, ",") {
			if t = strings.TrimSpace(t); t != "" {
				cfg.ExtraTopics = append(cfg.ExtraTopics, t)
			}
		}
		needReconnect = true
	default:
		fmt.Println("未知配置项，可用: mqtt | mqtt-port | server | token | base | feedback | server-name | extra")
		return
	}
	d.persist()
	if needReconnect {
		fmt.Println("正在重连", cfg.BrokerAddr(), "…")
		d.reconnect()
	}
}

func printConfig(cfg *config.Config) {
	fmt.Printf(`当前配置（文件 %s）:
  server        = %s
  token         = %s
  mqtt          = %s
  mqtt-port     = %d
  base          = %s
  feedback      = %s
  server-name   = %s
  extra         = %s
`,
		cfg.ConfigFile, cfg.ServerURL, mask(cfg.PushToken),
		cfg.MQTTBroker, cfg.MQTTPort, cfg.BaseTopic, cfg.FeedbackTopic,
		cfg.ServerName, strings.Join(cfg.ExtraTopics, ","))
}

func mask(s string) string {
	if s == "" {
		return "(空)"
	}
	return "****"
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
