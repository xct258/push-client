package mqttc

import "testing"

func TestParseTaskID(t *testing.T) {
	cases := map[string]int{
		"taskops/todo/7":   7,
		"taskops/todo/123": 123,
		"test/topic":       -1, // 尾段非数字
		"taskops/todo/0":   -1, // 非正数
		"taskops/todo/-3":  -1,
		"taskops/todo/":    -1,
		"":                 -1,
		"7":                7, // 单段数字也认
	}
	for topic, want := range cases {
		if got := ParseTaskID(topic); got != want {
			t.Errorf("ParseTaskID(%q)=%d, want %d", topic, got, want)
		}
	}
}

func TestBrokerURL(t *testing.T) {
	cases := []struct {
		opt  Option
		want string
	}{
		{Option{Broker: "h", Port: 1883}, "tcp://h:1883"},
		{Option{Broker: "h", Port: 1883, Scheme: "WS"}, "ws://h:1883/mqtt"},
		{Option{Broker: "h", Port: 9001, Scheme: "wss", Path: "mqtt"}, "wss://h:9001/mqtt"},
		{Option{Broker: "h", Port: 9001, Scheme: "wss", Path: ""}, "wss://h:9001/mqtt"},
		{Option{Broker: "h", Port: 8883, Scheme: "ssl"}, "ssl://h:8883"},
		{Option{Broker: "h", Port: 1, Scheme: "bogus"}, "tcp://h:1"},
	}
	for _, c := range cases {
		if got := New(c.opt).BrokerURL(); got != c.want {
			t.Errorf("BrokerURL(%+v)=%s, want %s", c.opt, got, c.want)
		}
	}
}

func TestTLSConfig(t *testing.T) {
	c := New(Option{Broker: "h", TLSInsecure: true})
	tc, err := c.tlsConfig()
	if err != nil || !tc.InsecureSkipVerify {
		t.Fatalf("insecure 配置错误: %v %v", tc, err)
	}
	c2 := New(Option{Broker: "h", TLSCAFile: "/nonexistent-ca.pem"})
	if _, err := c2.tlsConfig(); err == nil {
		t.Fatal("不存在的 CA 文件应报错")
	}
}

func TestNormalizeTask(t *testing.T) {
	m := Normalize("taskops/todo/7", []byte("【待办提醒】交电费"), "taskops/todo")
	if !m.IsTaskMsg || m.TaskID != 7 {
		t.Errorf("任务下行识别失败: %+v", m)
	}
	m2 := Normalize("test/topic", []byte("hello"), "taskops/todo")
	if m2.IsTaskMsg || m2.TaskID != -1 {
		t.Errorf("通用推送不应标为任务消息: %+v", m2)
	}
}
