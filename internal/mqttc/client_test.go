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
