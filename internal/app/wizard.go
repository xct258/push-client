package app

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/xct258/push-client/internal/config"
)

// IsTerminal stdin 是否接在控制台上（管道/重定向时返回 false）。
func IsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}

// SetupWizard 初次使用交互配置向导：逐项回车确认，完成后保存到 exe 同目录 config.yaml。
// 非终端（管道/服务）下直接返回，不阻塞。
func SetupWizard(cfg *config.Config) {
	if !IsTerminal() {
		return
	}
	r := bufio.NewReader(os.Stdin)
	fmt.Println("=== 初次使用配置向导（直接回车=使用[]内默认值）===")
	cfg.ServerURL = strings.TrimRight(ask(r, "push-server HTTP 地址", cfg.ServerURL), "/")
	cfg.MQTTBroker = ask(r, "MQTT broker 地址", cfg.MQTTBroker)
	cfg.MQTTPort = askInt(r, "MQTT 端口", cfg.MQTTPort)
	cfg.BaseTopic = strings.Trim(ask(r, "下行基主题", cfg.BaseTopic), "/")
	cfg.FeedbackTopic = strings.Trim(ask(r, "上行反馈主题", cfg.FeedbackTopic), "/")
	cfg.ServerName = ask(r, "补拉 server_name", cfg.ServerName)
	if err := config.Save(cfg.ConfigFile, cfg); err != nil {
		fmt.Println("保存配置失败:", err)
		return
	}
	fmt.Println("配置已保存到", cfg.ConfigFile, "，以后直接运行即可，也可用 set 命令随时修改。")
}

func ask(r *bufio.Reader, label, def string) string {
	fmt.Printf("%s [%s]: ", label, def)
	line, _ := r.ReadString('\n')
	if s := strings.TrimSpace(line); s != "" {
		return s
	}
	return def
}

func askInt(r *bufio.Reader, label string, def int) int {
	fmt.Printf("%s [%d]: ", label, def)
	line, _ := r.ReadString('\n')
	if s := strings.TrimSpace(line); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 65535 {
			return n
		}
		fmt.Println("输入非法，保持默认值", def)
	}
	return def
}
