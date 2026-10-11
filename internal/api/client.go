package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client push-server REST 客户端。覆盖服务端全部公开接口：
// /api/health, /records, /api/push, /api/repush, /api/tasks, /api/task*, /api/task/{id}/feedback
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) auth(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("X-Push-Token", c.Token)
	}
}

func (c *Client) do(req *http.Request, out any) error {
	c.auth(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("解析响应失败: %w (原文 %.100s)", err, string(body))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ---- 健康检查 ----

func (c *Client) Health() (map[string]any, error) {
	req, _ := http.NewRequest("GET", c.BaseURL+"/api/health", nil)
	var out map[string]any
	return out, c.do(req, &out)
}

// ---- 通用推送记录 ----

type Record struct {
	ID         int    `json:"id"`
	ServerName string `json:"server_name"`
	Topic      string `json:"topic"`
	Message    string `json:"message"`
	PushToMQTT *bool  `json:"push_to_mqtt"`
	PushResult string `json:"push_result"`
	MsgType    string `json:"msg_type"`
	Mode       string `json:"mode"`
	CreatedAt  string `json:"created_at"`
}

type RecordsResp struct {
	Total   int      `json:"total"`
	Offset  int      `json:"offset"`
	Limit   int      `json:"limit"`
	Records []Record `json:"records"`
}

func (c *Client) Records(serverName, topic string, limit, offset int) (*RecordsResp, error) {
	q := url.Values{}
	if serverName != "" {
		q.Set("server_name", serverName)
	}
	if topic != "" {
		q.Set("topic", topic)
	}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	req, _ := http.NewRequest("GET", c.BaseURL+"/records?"+q.Encode(), nil)
	var out RecordsResp
	return &out, c.do(req, &out)
}

type PushReq struct {
	ServerName string `json:"server_name"`
	Topic      string `json:"topic"`
	Message    string `json:"message"`
	PushToMQTT bool   `json:"push_to_mqtt"`
	MsgType    string `json:"msg_type"`
	Mode       string `json:"mode"`
}

func (c *Client) Push(r PushReq) (map[string]any, error) {
	if r.MsgType == "" {
		r.MsgType = "markdown"
	}
	if r.Mode == "" {
		r.Mode = "append"
	}
	buf, _ := json.Marshal(r)
	req, _ := http.NewRequest("POST", c.BaseURL+"/api/push", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	var out map[string]any
	return out, c.do(req, &out)
}

func (c *Client) Repush(serverName string, id int) (map[string]any, error) {
	buf, _ := json.Marshal(map[string]any{"server_name": serverName, "id": id})
	req, _ := http.NewRequest("POST", c.BaseURL+"/api/repush", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	var out map[string]any
	return out, c.do(req, &out)
}

// ---- 定时任务（TaskOps 双向同步）----

type Task struct {
	TaskID     int    `json:"task_id"`
	ServerName string `json:"server_name"`
	ClientRef  string `json:"client_ref"`
	Topic      string `json:"topic"`
	Title      string `json:"title"`
	Message    string `json:"message"`
	ScheduleAt string `json:"schedule_at"`
	Recurring  bool   `json:"recurring"`
	Status     string `json:"status"`
	Callback   string `json:"callback"`
	Retry      int    `json:"retry"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

type TasksResp struct {
	Total int    `json:"total"`
	Tasks []Task `json:"tasks"`
}

// Tasks 查询定时任务，status 可选 pending/sent/done/cancelled。
func (c *Client) Tasks(serverName, status string, limit int) (*TasksResp, error) {
	q := url.Values{}
	if serverName != "" {
		q.Set("server_name", serverName)
	}
	if status != "" {
		q.Set("status", status)
	}
	if limit <= 0 {
		limit = 100
	}
	q.Set("limit", strconv.Itoa(limit))
	req, _ := http.NewRequest("GET", c.BaseURL+"/api/tasks?"+q.Encode(), nil)
	var out TasksResp
	return &out, c.do(req, &out)
}

// Feedback REST 备用反馈通道，与 MQTT 上行同一语义。
func (c *Client) Feedback(taskID int, action string, snoozeMinutes int) (map[string]any, error) {
	if snoozeMinutes <= 0 {
		snoozeMinutes = 60
	}
	buf, _ := json.Marshal(map[string]any{"action": action, "snooze_minutes": snoozeMinutes})
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/task/%d/feedback", c.BaseURL, taskID), bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	var out map[string]any
	return out, c.do(req, &out)
}
