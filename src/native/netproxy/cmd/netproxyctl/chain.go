package main

import (
	"context"
	"os"

	moduleapp "github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/module"
)

// chain 管理按网络自动切换的链路。
//
// 用法：
//
//	netproxyctl chain                 显示当前链路状态
//	netproxyctl chain list            列出全部链路
//	netproxyctl chain select <名称>   手动锁定一条链路
//	netproxyctl chain enable <名称>   启用链路
//	netproxyctl chain disable <名称>  停用链路
//	netproxyctl chain on|off          链路管理总开关
func (c *cli) chain(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return c.chainStatus(ctx)
	}
	switch args[0] {
	case "status":
		return c.chainStatus(ctx)
	case "list":
		return c.chainList(ctx)
	case "select":
		return c.chainSelect(ctx, args[1:])
	case "enable", "disable":
		return c.chainToggle(ctx, args[1:], args[0] == "enable")
	case "on", "off":
		return c.chainMaster(ctx, args[1:], args[0] == "on")
	default:
		return usageError("chain 支持 status、list、select、enable、disable、on、off")
	}
}

func (c *cli) chainStatus(ctx context.Context) error {
	state, err := moduleapp.ReadChainStatus(ctx, c.options)
	if err != nil {
		return err
	}
	writeJSON(os.Stdout, result{Schema: 1, OK: true, Code: "chain.status", Message: "当前链路状态", Data: state})
	return nil
}

func (c *cli) chainList(ctx context.Context) error {
	chains, err := moduleapp.ListChains(ctx, c.options)
	if err != nil {
		return err
	}
	writeJSON(os.Stdout, result{Schema: 1, OK: true, Code: "chain.list", Message: "链路列表", Data: chains})
	return nil
}

func (c *cli) chainSelect(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return usageError("chain select 需要一个链路名称，使用 auto 恢复自动选择")
	}
	state, err := moduleapp.SelectChain(ctx, c.options, args[0])
	if err != nil {
		return err
	}
	writeJSON(os.Stdout, result{Schema: 1, OK: true, Code: "chain.selected", Message: "链路已切换", Data: state})
	return nil
}

func (c *cli) chainToggle(ctx context.Context, args []string, enabled bool) error {
	if len(args) != 1 {
		return usageError("chain enable 与 chain disable 需要一个链路名称")
	}
	state, err := moduleapp.SetChainEnabled(ctx, c.options, args[0], enabled)
	if err != nil {
		return err
	}
	writeJSON(os.Stdout, result{Schema: 1, OK: true, Code: "chain.toggled", Message: "链路开关已更新", Data: state})
	return nil
}

func (c *cli) chainMaster(ctx context.Context, args []string, enabled bool) error {
	if len(args) != 0 {
		return usageError("chain on 与 chain off 不接受额外参数")
	}
	state, err := moduleapp.SetChainsEnabled(ctx, c.options, enabled)
	if err != nil {
		return err
	}
	writeJSON(os.Stdout, result{Schema: 1, OK: true, Code: "chain.master", Message: "链路管理开关已更新", Data: state})
	return nil
}
