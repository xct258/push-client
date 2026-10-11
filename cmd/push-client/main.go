// Command push-client 是 push-server 的配套 Windows 客户端。
//
// 协议依据：
//
//	https://github.com/xct258/push-server  README + MQTT客户端设计.md + MQTT提醒双向同步方案.md
//
// 用法：
//
//	push-client [全局旗标] <命令> [命令参数]
//	push-client daemon                      # 常驻订阅+弹窗+补拉（默认）
//	push-client tasks [--status pending]    # 查询定时任务
//	push-client records                     # 查询推送记录
//	push-client done <task_id>              # 反馈完成
//	push-client snooze <task_id> [分钟]     # 反馈推迟
//	push-client push -topic T -message M    # 测试推送（调 /api/push）
//	push-client health                      # 健康检查
//
// 全局旗标（也可走 config.yaml / 环境变量，优先级：旗标 > 环境 > 文件）：
//
//	-server, -mqtt, -mqtt-port, -base, -feedback, -server-name, -token, -extra, -config
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/xct258/push-client/internal/api"
	"github.com/xct258/push-client/internal/app"
	"github.com/xct258/push-client/internal/config"
	"github.com/xct258/push-client/internal/mqttc"
)

func usage() {
	fmt.Fprintf(os.Stderr, `push-client — push-server 配套 Windows 客户端 (Go)

用法:
  push-client [全局旗标] <命令> [参数]

命令:
  daemon                  常驻订阅+弹窗+补拉+交互回执（默认）
  tasks                   查询定时任务
  records                 查询推送记录
  done <task_id>          反馈完成
  snooze <task_id> [分钟] 反馈推迟（默认60）
  push                    测试推送到 /api/push
  health                  健康检查

全局旗标:
  -server ADDR     push-server 地址 (默认 http://127.0.0.1:8383)
  -mqtt HOST       MQTT broker (默认 127.0.0.1)
  -mqtt-port PORT  MQTT 端口 (默认 1883)
  -base TOPIC      下行基主题 (默认 taskops/todo)
  -feedback TOPIC  上行反馈主题 (默认 taskops/feedback)
  -server-name N   补拉过滤 (默认 taskops)
  -token T         X-Push-Token
  -extra T1,T2     附加订阅主题
  -config PATH     配置文件 (默认 config.yaml)
  -no-notify       只打印不弹窗

示例:
  push-client daemon
  push-client -server http://192.168.1.10:8383 -mqtt 192.168.1.10 daemon
  push-client tasks --status pending
  push-client done 7
  push-client snooze 7 30
`)
}

func main() {
	// 无子命令默认 daemon（方便双击运行 push-client.exe）
	raw := os.Args[1:]
	if len(raw) == 0 {
		raw = []string{"daemon"}
	}
	// 切分子命令：第一个命中已知命令名的词即子命令（避免 -server <url> 的值被误判）。
	known := map[string]bool{
		"daemon": true, "tasks": true, "records": true, "done": true,
		"snooze": true, "push": true, "health": true, "help": true,
		"-h": true, "--help": true, "-help": true,
	}
	split := len(raw)
	for i, a := range raw {
		if known[a] {
			split = i
			break
		}
	}
	globalArgs, tail := raw[:split], raw[split:]
	var cmd string
	var cmdArgs []string
	if len(tail) > 0 {
		cmd, cmdArgs = tail[0], tail[1:]
		// daemon 后可跟 --no-notify 等：并回全局解析
		if cmd == "daemon" {
			globalArgs = append(globalArgs, cmdArgs...)
			cmdArgs = nil
		}
	} else {
		// 全是旗标 -> 默认 daemon
		cmd = "daemon"
	}

	cfg, _, err := config.Load(globalArgs)
	if err != nil {
		if err == flag.ErrHelp {
			usage()
			return
		}
		fmt.Fprintln(os.Stderr, "配置错误:", err)
		os.Exit(2)
	}

	rest := api.New(cfg.ServerURL, cfg.PushToken)
	code := 0
	switch cmd {
	case "daemon":
		code = app.RunDaemon(cfg)
	case "health":
		out, err := rest.Health()
		if err != nil {
			fmt.Println("FAIL:", err)
			code = 1
		} else {
			fmt.Println("OK:", out)
		}
	case "tasks":
		code = runTasks(rest, cfg, cmdArgs)
	case "records":
		code = runRecords(rest, cmdArgs)
	case "done":
		code = runFeedback(cfg, rest, cmdArgs, "done")
	case "snooze":
		code = runFeedback(cfg, rest, cmdArgs, "snooze")
	case "push":
		code = runPush(rest, cmdArgs)
	case "help", "-h", "--help", "-help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q\n\n", cmd)
		usage()
		code = 2
	}
	os.Exit(code)
}

func runTasks(rest *api.Client, cfg *config.Config, args []string) int {
	fs := flag.NewFlagSet("tasks", flag.ContinueOnError)
	status := fs.String("status", "pending", "按状态筛选: pending/sent/done/cancelled/空=全部")
	server := fs.String("server-name", cfg.ServerName, "按服务筛选")
	limit := fs.Int("limit", 100, "返回条数")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	resp, err := rest.Tasks(*server, *status, *limit)
	if err != nil {
		fmt.Println("查询失败:", err)
		return 1
	}
	fmt.Printf("共 %d 个任务:\n", resp.Total)
	for _, t := range resp.Tasks {
		fmt.Printf("  #%d [%s] %s | ref=%s | 排期 %s | 回调 %s\n    %s\n",
			t.TaskID, t.Status, t.Title, t.ClientRef, t.ScheduleAt, t.Callback, oneLine(t.Message))
	}
	return 0
}

func runRecords(rest *api.Client, args []string) int {
	fs := flag.NewFlagSet("records", flag.ContinueOnError)
	server := fs.String("server-name", "", "按服务器筛选")
	topic := fs.String("topic", "", "按主题模糊筛选")
	limit := fs.Int("limit", 20, "返回条数")
	offset := fs.Int("offset", 0, "偏移量")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	resp, err := rest.Records(*server, *topic, *limit, *offset)
	if err != nil {
		fmt.Println("查询失败:", err)
		return 1
	}
	fmt.Printf("共 %d 条:\n", resp.Total)
	for _, r := range resp.Records {
		fmt.Printf("  #%d [%s] %s (%s): %s\n", r.ID, r.ServerName, r.Topic, r.CreatedAt, oneLine(r.Message))
	}
	return 0
}

// runFeedback 先走 MQTT 上行（broker 可达时），失败降级 REST 备用通道。
func runFeedback(cfg *config.Config, rest *api.Client, args []string, action string) int {
	if len(args) < 1 {
		fmt.Printf("用法: push-client %s <task_id> [分钟]\n", action)
		return 2
	}
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		fmt.Println("task_id 须为正整数")
		return 2
	}
	minutes := 60
	if action == "snooze" && len(args) >= 2 {
		if m, err := strconv.Atoi(args[1]); err == nil && m > 0 {
			minutes = m
		}
	}
	// 尝试 MQTT 上行
	mc := mqttc.New(mqttc.Option{
		Broker: cfg.MQTTBroker, Port: cfg.MQTTPort, ClientID: cfg.MQTTClientID,
		User: cfg.MQTTUser, Pass: cfg.MQTTPass, QoS: cfg.QoS,
		Topics: nil, FeedbackTopic: cfg.FeedbackTopic,
	})
	if err := mc.Connect(8 * time.Second); err == nil {
		defer mc.Disconnect()
		if err := mc.Feedback(id, action, minutes); err != nil {
			fmt.Println("MQTT 反馈失败，转 REST 备用:", err)
		} else {
			fmt.Printf("已发送 %s #%d（MQTT %s）\n", action, id, cfg.FeedbackTopic)
			return 0
		}
	} else {
		fmt.Println("MQTT 不可达，走 REST 备用通道:", err)
	}
	res, err := rest.Feedback(id, action, minutes)
	if err != nil {
		fmt.Println("REST 反馈失败:", err)
		return 1
	}
	fmt.Printf("已发送 %s #%d（REST 备用）: %v\n", action, id, res["status"])
	return 0
}

func runPush(rest *api.Client, args []string) int {
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	server := fs.String("server-name", "server-1", "来源服务器名称")
	topic := fs.String("topic", "test/topic", "MQTT 主题")
	message := fs.String("message", "你好，世界", "消息内容")
	push := fs.Bool("mqtt", true, "是否推送到 MQTT")
	mode := fs.String("mode", "append", "append 或 overwrite")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	out, err := rest.Push(api.PushReq{
		ServerName: *server, Topic: *topic, Message: *message,
		PushToMQTT: *push, MsgType: "markdown", Mode: *mode,
	})
	if err != nil {
		fmt.Println("推送失败:", err)
		return 1
	}
	fmt.Println("推送成功:", out)
	return 0
}

func oneLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len([]rune(s)) > 80 {
		return string([]rune(s)[:80]) + "…"
	}
	return s
}
