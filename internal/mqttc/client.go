package mqttc

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Message 归一化后的下行消息。
type Message struct {
	Topic     string // 完整主题
	TaskID    int    // 从主题尾段解析，-1 表示非任务消息（通用推送）
	Text      string // payload 纯文本，直接展示
	Raw       []byte // 原始 payload
	Received  time.Time
	IsTaskMsg bool // 是否命中 task 下行（主题在 base/# 下且尾段为整数）
}

// Handler 收到下行后的回调。
type Handler func(m Message)

// Client MQTT 订阅+发布封装：自动重连（指数退避由 paho 接管），
// 重连后自动补订阅，重连成功回调供上层触发 REST 补拉。
type Client struct {
	broker        string
	port          int
	clientID      string
	user, pass    string
	qos           byte
	topics        []string
	feedbackTopic string

	cli   mqtt.Client
	onMsg Handler
	onRec chan<- struct{}

	mu      sync.Mutex
	sentLog map[int]time.Time // 反馈节流：同一 task 短时间重复 snooze 告警
}

type Option struct {
	Broker        string
	Port          int
	ClientID      string
	User, Pass    string
	QoS           byte
	Topics        []string
	FeedbackTopic string
	OnMessage     Handler
	// OnReconnect 重连成功后触发（传 true）/ 首次连接成功（传 false），上层据此补拉。
	OnReconnect chan<- struct{}
}

func New(o Option) *Client {
	if o.ClientID == "" {
		o.ClientID = "push-client-" + randHex(6)
	}
	if o.QoS > 2 {
		o.QoS = 1
	}
	return &Client{
		broker: o.Broker, port: o.Port, clientID: o.ClientID,
		user: o.User, pass: o.Pass, qos: o.QoS,
		topics: o.Topics, feedbackTopic: o.FeedbackTopic,
		onMsg: o.OnMessage, onRec: o.OnReconnect,
		sentLog: map[int]time.Time{},
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	const hexd = "0123456789abcdef"
	out := make([]byte, n*2)
	for i, v := range b {
		out[i*2] = hexd[v>>4]
		out[i*2+1] = hexd[v&15]
	}
	return string(out)
}

// ParseTaskID 按服务端约定取主题最后一段为 task_id，取不到返回 -1。
func ParseTaskID(topic string) int {
	topic = strings.Trim(topic, "/")
	if topic == "" {
		return -1
	}
	last := topic[strings.LastIndex(topic, "/")+1:]
	id, err := strconv.Atoi(last)
	if err != nil || id <= 0 {
		return -1
	}
	return id
}

// Normalize 把原始 MQTT 消息转为展示结构。base 用于判定是否任务消息。
func Normalize(topic string, payload []byte, base string) Message {
	m := Message{Topic: topic, Raw: payload, Text: string(payload), Received: time.Now(), TaskID: -1}
	base = strings.Trim(base, "/")
	if base != "" && (topic == base || strings.HasPrefix(topic, base+"/")) {
		if id := ParseTaskID(topic); id > 0 {
			m.TaskID = id
			m.IsTaskMsg = true
		}
	} else if id := ParseTaskID(topic); id > 0 && strings.HasSuffix(topic, "/"+strconv.Itoa(id)) {
		// 附加主题下也尽量提取 task_id 供展示，但不标为任务消息（反馈仍可用 id）。
		m.TaskID = id
	}
	return m
}

// Connect 阻塞直到连上或超时。连接丢失后 paho 自动重连。
func (c *Client) Connect(timeout time.Duration) error {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", c.broker, c.port))
	opts.SetClientID(c.clientID)
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(60 * time.Second)
	opts.SetConnectTimeout(timeout)
	opts.SetKeepAlive(60 * time.Second)
	if c.user != "" {
		opts.SetUsername(c.user)
		opts.SetPassword(c.pass)
	}
	first := true
	opts.SetOnConnectHandler(func(_ mqtt.Client) {
		if len(c.topics) > 0 {
			log.Printf("[mqtt] 已连接 %s:%d，订阅 %v", c.broker, c.port, c.topics)
			for _, t := range c.topics {
				tk := c.cli.Subscribe(t, c.qos, c.route)
				if !tk.WaitTimeout(10*time.Second) || tk.Error() != nil {
					log.Printf("[mqtt] 订阅 %s 失败: %v", t, tk.Error())
				}
			}
		}
		if c.onRec != nil {
			select {
			case c.onRec <- struct{}{}:
			default:
			}
		}
		if !first {
			log.Printf("[mqtt] 重连成功，已补订阅")
		}
		first = false
	})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		log.Printf("[mqtt] 连接断开: %v，自动重连中…", err)
	})
	c.cli = mqtt.NewClient(opts)
	tk := c.cli.Connect()
	if !tk.WaitTimeout(timeout) {
		return fmt.Errorf("连接 MQTT 超时 %s:%d", c.broker, c.port)
	}
	return tk.Error()
}

func (c *Client) route(_ mqtt.Client, msg mqtt.Message) {
	// base 需要从 topics[0] 反推（首项恒为 base/#）。
	base := ""
	if len(c.topics) > 0 {
		base = strings.TrimSuffix(c.topics[0], "/#")
	}
	m := Normalize(msg.Topic(), msg.Payload(), base)
	if c.onMsg != nil {
		c.onMsg(m)
	}
}

// Feedback 发布上行反馈 done/snooze。snooze 本地节流：10s 内重复推迟直接返回错误提醒防连点
// （服务端重复 snooze 会累加顺延，见服务端文档第 4 节）。
func (c *Client) Feedback(taskID int, action string, snoozeMinutes int) error {
	if taskID <= 0 {
		return fmt.Errorf("task_id 非法: %d", taskID)
	}
	if action != "done" && action != "snooze" {
		return fmt.Errorf("action 只能是 done/snooze")
	}
	if action == "snooze" {
		if snoozeMinutes <= 0 {
			snoozeMinutes = 60
		}
		c.mu.Lock()
		if last, ok := c.sentLog[taskID]; ok && time.Since(last) < 10*time.Second {
			c.mu.Unlock()
			return fmt.Errorf("task #%d 10s 内已发送过反馈，防连点已拦截", taskID)
		}
		c.sentLog[taskID] = time.Now()
		c.mu.Unlock()
	}
	body, _ := json.Marshal(map[string]any{
		"task_id": taskID, "action": action, "snooze_minutes": snoozeMinutes,
	})
	tk := c.cli.Publish(c.feedbackTopic, c.qos, false, body)
	if !tk.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("发布反馈超时")
	}
	return tk.Error()
}

// PublishRaw 通用发布（调试用）。
func (c *Client) PublishRaw(topic string, payload []byte) error {
	tk := c.cli.Publish(topic, c.qos, false, payload)
	if !tk.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("发布超时")
	}
	return tk.Error()
}

func (c *Client) Disconnect() {
	if c.cli != nil && c.cli.IsConnected() {
		c.cli.Unsubscribe(c.topics...)
		c.cli.Disconnect(500)
	}
}

func (c *Client) Connected() bool {
	return c.cli != nil && c.cli.IsConnected()
}
