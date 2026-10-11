package config

import "testing"

func TestSubscribeTopics(t *testing.T) {
	c := Default()
	got := c.SubscribeTopics()
	if len(got) != 1 || got[0] != "taskops/todo/#" {
		t.Fatalf("默认订阅错误: %v", got)
	}
	c.ExtraTopics = []string{"test/topic", "taskops/todo/#", "notice/#"}
	got = c.SubscribeTopics()
	if len(got) != 3 || got[0] != "taskops/todo/#" || got[1] != "test/topic" || got[2] != "notice/#" {
		t.Fatalf("去重订阅错误: %v", got)
	}
}

func TestLoadPrecedence(t *testing.T) {
	t.Setenv("PUSH_SERVER_URL", "http://env:8383")
	cfg, _, err := Load([]string{"-server", "http://flag:8383"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerURL != "http://flag:8383" {
		t.Fatalf("旗标应覆盖环境变量，实际 %s", cfg.ServerURL)
	}
}
