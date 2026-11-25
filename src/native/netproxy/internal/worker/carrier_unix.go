//go:build linux || android

package worker

import (
	"context"
	"os/exec"
	"strings"
)

// carrierProps 按默认数据卡优先级读取运营商属性。
//
// 双 SIM / eSIM 场景以系统默认数据卡（data 卡槽）为准，读取失败时不阻塞网络评估，
// 只是缺少 MCC/MNC，链路匹配会退化为按类型与 SSID 匹配。
var carrierProps = []string{
	"gsm.sim.operator.numeric",
	"gsm.operator.numeric",
	"gsm.sim.operator.iso-country",
}

// readCarrierInfo 返回默认数据卡的 MCC、MNC 与 ICCID。
func readCarrierInfo(ctx context.Context) (mcc, mnc, iccid string) {
	numeric := readCarrierNumeric(ctx)
	if len(numeric) >= 5 {
		// MCC 固定三位，其余为 MNC（两位或三位）。
		mcc, mnc = numeric[:3], numeric[3:]
	}
	return mcc, mnc, readCarrierICCID(ctx)
}

// readCarrierNumeric 优先读取带默认 subscriptions 的 numeric 属性。
func readCarrierNumeric(ctx context.Context) string {
	for _, slot := range defaultDataSlots(ctx) {
		for _, prop := range carrierProps {
			value := strings.TrimSpace(getprop(ctx, prop+slot))
			if numeric := normalizeNumeric(value); numeric != "" {
				return numeric
			}
		}
	}
	for _, prop := range carrierProps {
		if numeric := normalizeNumeric(strings.TrimSpace(getprop(ctx, prop))); numeric != "" {
			return numeric
		}
	}
	return ""
}

// defaultDataSlots 返回默认数据卡的属性后缀，按优先级排列。
//
// Android 的 props 既可能带 .<slot> 后缀，也可能只有全局值，
// 因此先读带后缀的属性，再回退到全局属性。
func defaultDataSlots(ctx context.Context) []string {
	slots := make([]string, 0, 2)
	for _, prop := range []string{"persist.radio.data_sub", "ro.vendor.radio.data_sub", "gsm.sim.active"} {
		value := strings.TrimSpace(getprop(ctx, prop))
		if value == "" {
			continue
		}
		// 属性可能是单值也可能以逗号分隔的订阅列表。
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			slots = append(slots, "."+item)
		}
		if len(slots) > 0 {
			return slots
		}
	}
	return nil
}

// normalizeNumeric 只保留运营商代码中的数字，并校验长度。
func normalizeNumeric(value string) string {
	var builder strings.Builder
	for _, char := range value {
		if char >= '0' && char <= '9' {
			builder.WriteRune(char)
		}
	}
	numeric := builder.String()
	if len(numeric) < 5 || len(numeric) > 6 {
		return ""
	}
	return numeric
}

// readCarrierICCID 读取默认数据卡的 ICCID，用于号段匹配。
func readCarrierICCID(ctx context.Context) string {
	for _, slot := range defaultDataSlots(ctx) {
		value := strings.TrimSpace(getprop(ctx, "gsm.sim.iccid"+slot))
		if value != "" {
			return value
		}
	}
	return strings.TrimSpace(getprop(ctx, "gsm.sim.iccid"))
}

// getprop 调用 Android 属性服务，超时由调用方 context 控制。
func getprop(ctx context.Context, name string) string {
	if name == "" {
		return ""
	}
	command := exec.CommandContext(ctx, "getprop", name)
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return string(output)
}
