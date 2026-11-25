package androidplatform

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/contract"
)

func TestDecodeResponseDoesNotExposePlatformError(t *testing.T) {
	_, err := decodeResponse([]byte(`{"schema":1,"ok":false,"code":"android.query_failed","message":"secret=private-fixture https://invalid.example/subscription"}`), errors.New("private-process-error"))
	failure, ok := errors.AsType[*contract.Error](err)
	if !ok || failure.Code != "android.query_failed" || failure.Message != "无法读取 Android 平台信息" {
		t.Fatal(err)
	}
}

func TestDecodeResponsePreservesPlatformContracts(t *testing.T) {
	for _, test := range []struct {
		output string
		runErr error
		code   string
	}{
		{"", errors.New("process failed"), "android.unavailable"},
		{"{}", nil, "android.invalid_output"},
		{`{"schema":1,"ok":true,"code":"android.apps","unexpected":true}`, nil, "android.invalid_output"},
		{`{"schema":2,"ok":false,"code":"android.query_failed"}`, nil, "android.invalid_output"},
		{`{"schema":1,"ok":false,"code":"android.query_failed"}`, nil, "android.query_failed"},
		{`{"schema":1,"ok":true,"code":"android.apps","data":{"users":[]}}`, errors.New("process failed"), "android.query_failed"},
	} {
		_, err := decodeResponse([]byte(test.output), test.runErr)
		failure, ok := errors.AsType[*contract.Error](err)
		if !ok || failure.Code != test.code {
			t.Fatalf("want %s, got %v", test.code, err)
		}
	}
	data, err := decodeResponse([]byte(`{"schema":1,"ok":true,"code":"android.apps","data":{"users":[]}}`), nil)
	if err != nil || string(data) != `{"users":[]}` {
		t.Fatal(string(data), err)
	}
}

func TestFramePrefixesDexAndKeepsRequestIntact(t *testing.T) {
	dex := []byte{0x64, 0x65, 0x78, 0x0a}
	request := []byte(`{"operation":"provision"}`)
	frame := Frame(dex, request)
	if len(frame) != 4+len(dex)+len(request) {
		t.Fatalf("frame length %d", len(frame))
	}
	if size := binary.BigEndian.Uint32(frame[:4]); int(size) != len(dex) {
		t.Fatalf("dex size prefix %d", size)
	}
	if string(frame[4:4+len(dex)]) != string(dex) || string(frame[4+len(dex):]) != string(request) {
		t.Fatal("frame payload mismatch")
	}
}
