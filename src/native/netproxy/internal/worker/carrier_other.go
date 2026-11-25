//go:build !(linux || android)

package worker

import "context"

// readCarrierInfo 在非 Android 平台不提供运营商信息。
func readCarrierInfo(context.Context) (mcc, mnc, iccid string) { return "", "", "" }
