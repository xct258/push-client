package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Version 客户端版本（启动横幅、version 命令、GUI 共用，确认跑的是不是最新构建）。
const Version = "v0.5.0"

// Config Windows 客户端全部配置。优先级：命令行旗标 > 环境变量 > 配置文件 > 默认值。
type Config struct {
	ServerURL     string   `yaml:"server_url"`        // push-server HTTP 地址，如 http://127.0.0.1:8383
	PushToken     string   `yaml:"push_token"`        // 写接口鉴权 X-Push-Token，服务端没配则留空
	MQTTBroker    string   `yaml:"mqtt_broker"`       // MQTT 地址，默认 127.0.0.1
	MQTTPort      int      `yaml:"mqtt_port"`         // MQTT 端口，tcp 默认 1883，ws/wss 常用 9001
	MQTTScheme    string   `yaml:"mqtt_scheme"`       // tcp | ws | wss，默认 tcp
	MQTTPath      string   `yaml:"mqtt_path"`         // ws/wss 的 HTTP 路径，默认 /mqtt
	MQTTTLSInsec  bool     `yaml:"mqtt_tls_insecure"` // wss 自签证书时跳过校验（仅测试/内网用）
	MQTTCAFile    string   `yaml:"mqtt_ca_file"`      // wss 自定义 CA 证书文件（与 insecure 互斥）
	MQTTClientID  string   `yaml:"mqtt_client_id"`    // MQTT 客户端 ID，空则自动生成
	MQTTUser      string   `yaml:"mqtt_user"`
	MQTTPass      string   `yaml:"mqtt_password"`
	BaseTopic     string   `yaml:"base_topic"`     // 下行基主题，默认 taskops/todo，订阅 base/#
	FeedbackTopic string   `yaml:"feedback_topic"` // 上行反馈主题，默认 taskops/feedback
	ExtraTopics   []string `yaml:"extra_topics"`   // 通用推送附加订阅，如 ["test/topic", "notice/#"]
	QoS           byte     `yaml:"qos"`            // 上下行 QoS，默认 1
	ServerName    string   `yaml:"server_name"`    // 补拉过滤 server_name，默认 taskops
	NotifyCmd     string   `yaml:"notify_cmd"`     // 收到提醒后额外执行的外部命令（可选，透传标题正文）
	NoNotify      bool     `yaml:"no_notify"`      // 只打印不弹窗（调试用）

	// 以下不落盘：运行态
	ConfigFile  string `yaml:"-"`
	ConfigFound bool   `yaml:"-"`
	ForceSetup  bool   `yaml:"-"`
	NoSetup     bool   `yaml:"-"`
}

func Default() *Config {
	return &Config{
		ServerURL:     "http://127.0.0.1:8383",
		MQTTBroker:    "127.0.0.1",
		MQTTPort:      1883,
		MQTTScheme:    "tcp",
		MQTTPath:      "/mqtt",
		BaseTopic:     "taskops/todo",
		FeedbackTopic: "taskops/feedback",
		ExtraTopics:   nil,
		QoS:           1,
		ServerName:    "taskops",
	}
}

// configFileFlag 预解析 -config 参数，避免 flag 包二次定义冲突。
func configFileFlag(args []string) string {
	for i, a := range args {
		if a == "-config" || a == "--config" {
			if i+1 < len(args) {
				return args[i+1]
			}
		}
		if strings.HasPrefix(a, "-config=") {
			return strings.TrimPrefix(a, "-config=")
		}
		if strings.HasPrefix(a, "--config=") {
			return strings.TrimPrefix(a, "--config=")
		}
	}
	return "config.yaml"
}

// resolveFile 定位配置文件：显式 -config 优先；默认名则先找 exe 同目录，再找当前目录。
// 找不到时返回建议的保存位置（exe 同目录，Windows 双击场景最稳）。
func resolveFile(explicit string) (path string, found bool) {
	if explicit != "" && explicit != "config.yaml" {
		if _, err := os.Stat(explicit); err == nil {
			return explicit, true
		}
		return explicit, false
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "config.yaml")
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	if _, err := os.Stat("config.yaml"); err == nil {
		return "config.yaml", true
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "config.yaml"), false
	}
	return "config.yaml", false
}

// Save 把当前配置持久化到 path（供向导与 set 命令调用）。
func Save(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}
	return os.WriteFile(path, data, 0644)
}

// Load 按“配置文件 -> 环境变量 -> 命令行”顺序合并。args 传 os.Args[1:]。
func Load(args []string) (*Config, *flag.FlagSet, error) {
	cfg := Default()

	// 1. 配置文件（不存在不报错；默认名优先找 exe 同目录）
	cfgFile := configFileFlag(args)
	cfgFile, cfg.ConfigFound = resolveFile(cfgFile)
	cfg.ConfigFile = cfgFile
	if data, err := os.ReadFile(cfgFile); err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, nil, fmt.Errorf("解析 %s 失败: %w", cfgFile, err)
		}
	}

	// 2. 环境变量
	if v := os.Getenv("PUSH_SERVER_URL"); v != "" {
		cfg.ServerURL = v
	}
	if v := os.Getenv("PUSH_SERVER_TOKEN"); v != "" {
		cfg.PushToken = v
	}
	if v := os.Getenv("MQTT_BROKER"); v != "" {
		cfg.MQTTBroker = v
	}
	if v := os.Getenv("MQTT_PORT"); v != "" {
		var p int
		if _, err := fmt.Sscanf(v, "%d", &p); err == nil && p > 0 {
			cfg.MQTTPort = p
		}
	}
	if v := os.Getenv("MQTT_SCHEME"); v != "" {
		cfg.MQTTScheme = v
	}
	if v := os.Getenv("MQTT_PATH"); v != "" {
		cfg.MQTTPath = v
	}
	if v := os.Getenv("MQTT_TLS_INSECURE"); v == "1" || strings.EqualFold(v, "true") {
		cfg.MQTTTLSInsec = true
	}
	if v := os.Getenv("MQTT_CA_FILE"); v != "" {
		cfg.MQTTCAFile = v
	}
	if v := os.Getenv("PUSH_BASE_TOPIC"); v != "" {
		cfg.BaseTopic = v
	}
	if v := os.Getenv("PUSH_FEEDBACK_TOPIC"); v != "" {
		cfg.FeedbackTopic = v
	}

	// 3. 命令行旗标（覆盖前两级）
	fs := flag.NewFlagSet("push-client", flag.ContinueOnError)
	fs.StringVar(&cfg.ServerURL, "server", cfg.ServerURL, "push-server HTTP 地址")
	fs.StringVar(&cfg.PushToken, "token", cfg.PushToken, "写接口 X-Push-Token")
	fs.StringVar(&cfg.MQTTBroker, "mqtt", cfg.MQTTBroker, "MQTT broker 地址")
	fs.IntVar(&cfg.MQTTPort, "mqtt-port", cfg.MQTTPort, "MQTT 端口")
	fs.StringVar(&cfg.MQTTScheme, "mqtt-scheme", cfg.MQTTScheme, "MQTT 协议: tcp | ws | wss")
	fs.StringVar(&cfg.MQTTPath, "mqtt-path", cfg.MQTTPath, "ws/wss 的 HTTP 路径")
	fs.BoolVar(&cfg.MQTTTLSInsec, "mqtt-tls-insecure", cfg.MQTTTLSInsec, "wss 自签证书跳过校验")
	fs.StringVar(&cfg.MQTTCAFile, "mqtt-ca", cfg.MQTTCAFile, "wss 自定义 CA 证书文件")
	fs.StringVar(&cfg.MQTTClientID, "client-id", cfg.MQTTClientID, "MQTT 客户端ID(空=自动)")
	fs.StringVar(&cfg.MQTTUser, "mqtt-user", cfg.MQTTUser, "MQTT 用户名")
	fs.StringVar(&cfg.MQTTPass, "mqtt-pass", cfg.MQTTPass, "MQTT 密码")
	fs.StringVar(&cfg.BaseTopic, "base", cfg.BaseTopic, "下行基主题")
	fs.StringVar(&cfg.FeedbackTopic, "feedback", cfg.FeedbackTopic, "上行反馈主题")
	fs.StringVar(&cfg.ServerName, "server-name", cfg.ServerName, "补拉 server_name 过滤")
	fs.StringVar(&cfg.NotifyCmd, "notify-cmd", cfg.NotifyCmd, "提醒后执行的外部命令")
	fs.BoolVar(&cfg.NoNotify, "no-notify", cfg.NoNotify, "只打印不弹窗")
	extra := fs.String("extra", strings.Join(cfg.ExtraTopics, ","), "附加订阅主题，逗号分隔")
	fs.BoolVar(&cfg.ForceSetup, "setup", false, "强制进入交互配置向导")
	fs.BoolVar(&cfg.NoSetup, "no-setup", false, "跳过交互配置向导（服务/自动化用）")
	// 允许 -config 旗标存在但忽略实际加载（已在步骤1处理）
	_ = fs.String("config", configFileFlag(args), "配置文件路径")
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	if *extra != "" {
		var out []string
		for _, t := range strings.Split(*extra, ",") {
			if t = strings.TrimSpace(t); t != "" {
				out = append(out, t)
			}
		}
		cfg.ExtraTopics = out
	}
	cfg.ServerURL = strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/")
	cfg.BaseTopic = strings.Trim(strings.TrimSpace(cfg.BaseTopic), "/")
	cfg.FeedbackTopic = strings.Trim(strings.TrimSpace(cfg.FeedbackTopic), "/")
	switch strings.ToLower(strings.TrimSpace(cfg.MQTTScheme)) {
	case "ws", "wss", "tcp":
		cfg.MQTTScheme = strings.ToLower(strings.TrimSpace(cfg.MQTTScheme))
	default:
		return nil, nil, fmt.Errorf("mqtt-scheme 只能是 tcp/ws/wss，实际 %q", cfg.MQTTScheme)
	}
	if strings.TrimSpace(cfg.MQTTPath) == "" {
		cfg.MQTTPath = "/mqtt"
	}
	if !strings.HasPrefix(cfg.MQTTPath, "/") {
		cfg.MQTTPath = "/" + cfg.MQTTPath
	}
	if cfg.MQTTPort <= 0 || cfg.MQTTPort > 65535 {
		return nil, nil, fmt.Errorf("mqtt-port 非法: %d", cfg.MQTTPort)
	}
	if cfg.QoS > 2 {
		cfg.QoS = 1
	}
	return cfg, fs, nil
}

// SubscribeTopics 返回实际订阅列表：base/# + 附加主题（去重）。
func (c *Config) SubscribeTopics() []string {
	seen := map[string]bool{}
	var out []string
	base := c.BaseTopic + "/#"
	out = append(out, base)
	seen[base] = true
	for _, t := range c.ExtraTopics {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// BrokerAddr 返回 host:port（兼容旧日志）。
func (c *Config) BrokerAddr() string {
	return fmt.Sprintf("%s:%d", c.MQTTBroker, c.MQTTPort)
}

// BrokerURL 返回完整连接地址，如 tcp://host:1883、wss://host:9001/mqtt。
func (c *Config) BrokerURL() string {
	scheme := strings.ToLower(strings.TrimSpace(c.MQTTScheme))
	path := strings.TrimSpace(c.MQTTPath)
	if path == "" {
		path = "/mqtt"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if scheme == "ws" || scheme == "wss" {
		return fmt.Sprintf("%s://%s:%d%s", scheme, c.MQTTBroker, c.MQTTPort, path)
	}
	return fmt.Sprintf("%s://%s:%d", scheme, c.MQTTBroker, c.MQTTPort)
}
