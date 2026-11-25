package main

import (
	"context"
	"os"

	moduleapp "github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/module"
)

func (c *cli) service(ctx context.Context, args []string) error {
	operation := "status"
	if len(args) > 0 {
		operation = args[0]
	}
	switch operation {
	case "status", "start", "stop", "restart", "reload", "check", "toggle", "pause", "resume":
	default:
		return usageError("用法: netproxyctl service status|start|stop|restart|reload|check|toggle|pause|resume")
	}
	// pause/resume 持久化代理总开关，使重启后仍保持用户选择。
	if operation == "pause" || operation == "resume" {
		state, err := moduleapp.SetProxyEnabled(ctx, c.options, operation == "resume")
		if err != nil {
			return err
		}
		writeJSON(os.Stdout, result{Schema: 1, OK: true, Code: "service." + operation, Message: "代理总开关已更新", Data: state})
		return nil
	}
	data, err := moduleapp.ManageService(ctx, c.options, operation)
	if err != nil {
		return err
	}
	message := "服务操作完成"
	responseData := any(data)
	if operation == "status" {
		message = "服务状态"
		// service.status 是 Android 与 WebUI 的既有公开契约，状态字段必须直接位于 data。
		responseData = data.Status
	}
	writeJSON(os.Stdout, result{Schema: 1, OK: true, Code: "service." + operation, Message: message, Data: responseData})
	return nil
}
