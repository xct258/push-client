# push-client — push-server 配套 Windows 客户端（Go）

配套服务端：https://github.com/xct258/push-server
协议依据：服务端 `README.md`（REST）＋ `MQTT客户端设计.md`（下行纯文本/上行反馈）＋ `MQTT提醒双向同步方案.md`（任务状态机/补拉）。

## 功能

- **常驻订阅**：`taskops/todo/#`（QoS 1）＋ 可配置的通用推送附加主题（如 `test/topic`）
- **系统 Toast 弹窗**：Windows 下走原生通知（beeep/SnoreToast），Linux/macOS 走各自通知中心
- **一键回执**：控制台 `done <id>` / `snooze <id> [分钟]`，先走 MQTT 上行 `taskops/feedback`，失败自动降级 REST `POST /api/task/{id}/feedback`
- **离线补拉**：启动与每次 MQTT 重连后自动 `GET /api/tasks?status=pending`，展示错过/未来 24h 的任务
- **防连点**：同一任务 10s 内重复 snooze 本地拦截（服务端重复 snooze 会累加顺延）
- **单文件**：`push-client.exe` 无运行时依赖，纯 Go（paho.mqtt.golang + beeep + yaml）

## 快速开始（Windows）

1. 从 Release 下载 `push-client.exe`，或本地编译（见下）。
2. 复制 `config.example.yaml` 为 `config.yaml`，改 `server_url` / `mqtt_broker` 为服务端地址：

```yaml
server_url: "http://192.168.1.10:8383"
mqtt_broker: "192.168.1.10"
```

3. 双击运行（无参数默认 `daemon`）。**首次运行会自动进入配置向导**，按提示输入
   push-server 地址和 MQTT broker 地址（直接回车=用默认值），配置保存到 exe 同目录
   `config.yaml`，以后不再询问：

```bat
push-client.exe daemon
push-client.exe config    :: 随时重新进向导改配置
```

4. 在软件内随时改配置（改 MQTT 地址不用重启，会自动重连）：

```
get                        :: 查看当前配置
set mqtt 192.168.1.10      :: 改 MQTT 地址并自动重连
set mqtt-port 1883
set server http://192.168.1.10:8383
quit
```

4. 收到提醒后，在控制台输入回执：

```
done 7          :: 完成任务 #7
snooze 7 30     :: 推迟任务 #7 30 分钟（默认 60）
tasks           :: 查看 pending 任务
records         :: 查看最新推送记录
quit            :: 退出
```

## 命令一览

```
push-client daemon                  常驻订阅+弹窗+补拉+交互回执（默认）
push-client tasks [--status pending] [--server-name taskops]
push-client records [--server-name S] [--topic T] [--limit 20]
push-client done <task_id>
push-client snooze <task_id> [分钟]
push-client push -topic test/topic -message "hello"
push-client health
```

全局旗标（优先级：旗标 > 环境变量 > config.yaml > 默认值）：

| 旗标 | 环境变量 | 默认 |
|---|---|---|
| `-server` | `PUSH_SERVER_URL` | `http://127.0.0.1:8383` |
| `-token` | `PUSH_SERVER_TOKEN` | 空 |
| `-mqtt` | `MQTT_BROKER` | `127.0.0.1` |
| `-mqtt-port` | `MQTT_PORT` | `1883` |
| `-base` | `PUSH_BASE_TOPIC` | `taskops/todo` |
| `-feedback` | `PUSH_FEEDBACK_TOPIC` | `taskops/feedback` |
| `-server-name` | — | `taskops` |
| `-extra` | — | 空（逗号分隔） |
| `-config` | — | `config.yaml` |
| `-no-notify` | — | false |

## 协议对照（与服务端一致）

- 下行主题 `{base}/{task_id}`（如 `taskops/todo/7`），payload 纯文本 `【待办提醒】标题\n正文`，零解析展示，尾段取整即 `task_id`
- 上行主题 `taskops/feedback`，`{"task_id":7,"action":"done"}` / `{"task_id":7,"action":"snooze","snooze_minutes":60}`，QoS 1
- 备用通道 `POST /api/task/{id}/feedback`，body 同上；补拉 `GET /api/tasks?status=pending&server_name=taskops`

## Windows 运行无反应？

1. 确认下载的是 `push-client.exe`（不是无后缀的 `push-client`，那是 Linux 版）。
   启动第一行会打印 `push-client v0.3.0`，对不上就是旧包，重新下载。
2. 右键 exe → 属性 → 勾选底部"解除锁定"（如有）→ 确定；SmartScreen 拦截点"更多信息→仍要运行"。
3. 用 `cmd` 进目录跑 `push-client.exe daemon`，看窗口第一屏的 HTTP/MQTT 地址，
   连不上会每 5 秒重试并打印原因，此时用 `set mqtt <正确地址>` 直接修正。
4. 检查杀毒软件隔离区，新下载的无签名 exe 常被误杀，可加白名单。

## 编译

```bash
# Linux 本地
go build -o push-client ./cmd/push-client

# Windows（任意平台交叉编译，产物即 exe，无需 Windows 机器）
GOOS=windows GOARCH=amd64 go build -o push-client.exe ./cmd/push-client
# 或直接运行：
./build.sh            # Linux/macOS，产出 push-client + push-client.exe
build-windows.bat     # Windows 双击，产出 push-client.exe
```

## 开机自启（Windows，可选）

把 `push-client.exe` + `config.yaml` 放同一目录，创建计划任务（登录时运行 `push-client.exe daemon`），
或用 `sc.exe` / NSSM 注册为服务。程序收到 SIGINT/SIGTERM 会优雅断开 MQTT。
