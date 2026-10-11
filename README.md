# push-client — push-server 配套 Windows 客户端（Go + GUI）

配套服务端：https://github.com/xct258/push-server
协议依据：服务端 `README.md`（REST）＋ `MQTT客户端设计.md`（下行纯文本/上行反馈）＋ `MQTT提醒双向同步方案.md`（任务状态机/补拉）。

## 使用（图形界面，双击即用）

1. 下载 `push-client.exe`（约 55MB，单文件，无需安装），双击运行。
2. 顶部四个页签：
   - **提醒**：收到的 MQTT 提醒列表，选中看正文，点**完成**/**推迟**（分钟可选 15/30/60/120/240）；
     新提醒自动弹系统通知，关闭窗口缩到托盘，托盘可显示/退出。
   - **定时任务**：pending 等任务列表，刷新查看，可直接完成/推迟。
   - **推送记录**：服务端 `/records` 历史，刷新查看。
   - **设置**：push-server 地址、MQTT 地址/端口、主题、token 都在这里改，
     点**保存并重连**即生效（MQTT 自动重连），另有测试 HTTP 连接按钮。
3. 配置自动存到 exe 同目录 `config.yaml`。中文字体自动用系统字体
   （Windows 等线/黑体），设置页底部显示当前字体；找不到时可用
   `PUSH_CLIENT_FONT` 环境变量指定一个 `.ttf` 字体文件。

命令行模式仍保留（无头/服务用）：`push-client.exe daemon`，
`tasks` / `records` / `done <id>` / `snooze <id> [分钟]` / `push` / `health` 照常用。

## 功能

- **图形界面**：提醒列表＋完成/推迟按钮、定时任务、推送记录、设置页、托盘最小化、系统通知
- **常驻订阅**：`taskops/todo/#`（QoS 1）＋ 可配置的通用推送附加主题（如 `test/topic`）
- **一键回执**：先走 MQTT 上行 `taskops/feedback`，失败自动降级 REST `POST /api/task/{id}/feedback`
- **离线补拉**：启动与每次 MQTT 重连后自动 `GET /api/tasks?status=pending`，错过/24h 内到期的进提醒列表
- **防连点**：同一任务 10s 内重复 snooze 本地拦截（服务端重复 snooze 会累加顺延）
- **单文件**：`push-client.exe` 无运行时依赖（Fyne + paho.mqtt.golang + beeep + yaml）
- **命令行模式保留**：`daemon`（常驻，无头/服务用）及 `tasks` / `records` / `done` / `snooze` / `push` / `health`

## 命令行速查（daemon / 一次性命令仍可用）

```
push-client daemon                  命令行常驻+交互回执（首次运行进配置向导）
push-client config                  交互修改配置并保存
push-client tasks [--status pending] [--server-name taskops]
push-client records [--server-name S] [--topic T] [--limit 20]
push-client done <task_id>          反馈完成
push-client snooze <task_id> [分钟] 反馈推迟（默认60）
push-client push -topic test/topic -message "hello"
push-client health

daemon 内命令: done <id> | snooze <id> [分钟] | tasks | records | get | set mqtt <地址> | quit
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
   启动第一行会打印 `push-client v0.4.0`，对不上就是旧包，重新下载。
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
