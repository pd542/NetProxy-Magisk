package config

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/processlock"
)

type ModuleConfig struct {
	AutoStart bool          `json:"auto_start"`
	WiFi      WiFiPolicy    `json:"wifi"`
	Proxy     ProxyPolicy   `json:"proxy"`
	Carrier   CarrierPolicy `json:"carrier"`
}

// ProxyPolicy 是代理总开关；关闭后内核停止并全量直连。
type ProxyPolicy struct {
	Enabled bool `json:"enabled"`
}

// CarrierPolicy 控制 SIM 运营商识别，是链路匹配的输入之一。
type CarrierPolicy struct {
	Enabled bool `json:"enabled"`
}

// DefaultProxyPolicy 的默认值保持内核自动启动。
func DefaultProxyPolicy() ProxyPolicy { return ProxyPolicy{Enabled: true} }

// DefaultCarrierPolicy 默认开启运营商识别，未插卡时不会产生匹配。
func DefaultCarrierPolicy() CarrierPolicy { return CarrierPolicy{Enabled: true} }

type WiFiPolicy struct {
	Enabled        bool     `json:"enabled"`
	Mode           string   `json:"mode"`
	Blacklist      []string `json:"blacklist"`
	Whitelist      []string `json:"whitelist"`
	ProxyOnNonWiFi bool     `json:"proxy_on_non_wifi"`
}

func (policy WiFiPolicy) Equal(other WiFiPolicy) bool {
	return policy.Enabled == other.Enabled && policy.Mode == other.Mode &&
		policy.ProxyOnNonWiFi == other.ProxyOnNonWiFi &&
		slices.Equal(policy.Blacklist, other.Blacklist) && slices.Equal(policy.Whitelist, other.Whitelist)
}

// DefaultModule 返回全新配置使用的唯一默认值集合。
func DefaultModule() ModuleConfig {
	return ModuleConfig{
		WiFi:    WiFiPolicy{Mode: "blacklist", Blacklist: []string{}, Whitelist: []string{}, ProxyOnNonWiFi: true},
		Proxy:   DefaultProxyPolicy(),
		Carrier: DefaultCarrierPolicy(),
	}
}

func LoadModule(path string) (ModuleConfig, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return ModuleConfig{}, err
	}
	return ParseModule(content)
}

func ParseModule(content []byte) (ModuleConfig, error) {
	// JSON null 会把布尔、字符串和结构体静默置零；模块配置不允许这种隐式重置。
	decoder := jsontext.NewDecoder(bytes.NewReader(content))
	for {
		token, err := decoder.ReadToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ModuleConfig{}, fmt.Errorf("模块 JSON 配置无效: %w", err)
		}
		if token.Kind() == 'n' {
			return ModuleConfig{}, errors.New("模块配置字段不能为 null")
		}
	}
	config := DefaultModule()
	if err := json.Unmarshal(content, &config, json.RejectUnknownMembers(true)); err != nil {
		return ModuleConfig{}, fmt.Errorf("模块 JSON 配置无效: %w", err)
	}
	return config, config.validate()
}

func (config ModuleConfig) validate() error {
	if config.WiFi.Mode != "blacklist" && config.WiFi.Mode != "whitelist" {
		return errors.New("wifi.mode 只能是 blacklist 或 whitelist")
	}
	for _, list := range []struct {
		field string
		ssids []string
	}{{"blacklist", config.WiFi.Blacklist}, {"whitelist", config.WiFi.Whitelist}} {
		seen := make(map[string]bool, len(list.ssids))
		for _, ssid := range list.ssids {
			if err := ValidateSSID(ssid); err != nil {
				return fmt.Errorf("wifi.%s: %w", list.field, err)
			}
			if seen[ssid] {
				return fmt.Errorf("wifi.%s 包含重复的 Wi-Fi 名称", list.field)
			}
			seen[ssid] = true
		}
	}
	return nil
}

func ValidateSSID(ssid string) error {
	if len(ssid) == 0 || len(ssid) > 32 || !utf8.ValidString(ssid) || strings.ContainsFunc(ssid, unicode.IsControl) {
		return errors.New("Wi-Fi 名称必须为 1 至 32 字节的 UTF-8 文本，不能包含控制字符")
	}
	return nil
}

// Editor 在显式持有文件锁期间完成配置读改写。
type Editor struct {
	*processlock.Lock
}

func Lock(ctx context.Context, path string) (*Editor, error) {
	lock, err := processlock.Acquire(ctx, path+".lock")
	if err != nil {
		return nil, err
	}
	return &Editor{Lock: lock}, nil
}

// SaveModule 原子写入模块配置，保持 0600 权限。
func SaveModule(path string, config ModuleConfig) error {
	if err := config.validate(); err != nil {
		return err
	}
	content, err := json.Marshal(config, jsontext.WithIndent("  "))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(content, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
