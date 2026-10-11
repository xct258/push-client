package notify

import (
	"fmt"

	"github.com/gen2brain/beeep"
)

// Notify 跨平台提醒：Windows 下走系统 Toast（beeep 底层即 SnoreToast），
// Linux/macOS 走各自通知中心。show 失败只记日志，不阻断主流程。
func Notify(title, message string) error {
	if title == "" {
		title = "MQTT 推送中心"
	}
	// beeep.Notify 自带应用名与图标参数，图标留空用默认。
	if err := beeep.Notify(title, message, ""); err != nil {
		return fmt.Errorf("系统通知失败: %w", err)
	}
	return nil
}

// Alert 阻塞式强提醒（带确认按钮），用于重要待办。普通提醒请用 Notify。
func Alert(title, message string) error {
	if title == "" {
		title = "待办提醒"
	}
	return beeep.Alert(title, message, "")
}
