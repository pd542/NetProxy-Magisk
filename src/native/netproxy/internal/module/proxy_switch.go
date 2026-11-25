package module

import (
	"context"

	moduleconfig "github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/config"
	"github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/paths"
	"github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/service"
)

// proxyPaused 判断代理总开关是否关闭；配置读取失败时按开启处理，避免误停代理。
func proxyPaused(options Options) (bool, error) {
	module, err := moduleconfig.LoadModule(options.ModuleConfig)
	if err != nil {
		return false, nil
	}
	return !module.Proxy.Enabled, nil
}

// SetProxyEnabled 设置代理总开关。
//
// 打开时启动内核并立即按当前网络应用链路；关闭时停止内核并清理其路由，
// 系统回到直连状态。
func SetProxyEnabled(ctx context.Context, options Options, enabled bool) (ProxyState, error) {
	locked, release, err := lockConfigFiles(ctx, options, proxySwitchPaths(options)...)
	if err != nil {
		return ProxyState{}, err
	}
	defer release()
	if err := setProxyEnabledLocked(locked, enabled); err != nil {
		return ProxyState{}, err
	}
	state := ProxyState{Enabled: enabled}
	if !enabled {
		if err := StopService(ctx, locked); err != nil {
			return state, err
		}
	} else {
		if err := StartService(ctx, locked); err != nil {
			return state, err
		}
	}
	running := service.ProcessRunning(locked.SingBoxPath)
	state.Running = running
	return state, nil
}

// ProxyState 是总开关的执行结果。
type ProxyState struct {
	Enabled bool `json:"enabled"`
	Running bool `json:"running"`
}

// setProxyEnabledLocked 在已持有配置锁时写入总开关。
func setProxyEnabledLocked(options Options, enabled bool) error {
	module, err := moduleconfig.LoadModule(options.ModuleConfig)
	if err != nil {
		return err
	}
	module.Proxy.Enabled = enabled
	return moduleconfig.SaveModule(options.ModuleConfig, module)
}

// proxySwitchPaths 返回写代理开关需要同时锁定的配置路径。
func proxySwitchPaths(options Options) []string {
	return []string{
		options.ModuleConfig,
		options.InboundConfig,
		paths.SingBoxConfig(options.SingBoxDir),
		chainsPath(options),
	}
}
