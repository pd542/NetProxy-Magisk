package androidplatform

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/apiconfig"
	"github.com/Fanju6/NetProxy-Magisk/src/native/netproxy/internal/contract"
)

type Request struct {
	Operation  string                `json:"operation"`
	IconCache  string                `json:"icon_cache,omitempty"`
	Connection *apiconfig.Connection `json:"connection,omitempty"`
}

// Client 以 root 身份启动一个 app_process 子进程读取 Android 平台信息。
//
// DEX 不通过 -Djava.class.path 传路径：部分厂商（vivo 等）在 DexFile.openDexFile
// 中植入动态加载检测，加载外部 dex 时会在 JNI 就绪前抛异常并结束进程。
// 这里把 dex 字节直接写进 stdin，由内置引导类用 InMemoryDexClassLoader
// 从内存加载，全程不经过文件式 dex 加载路径。
type Client struct {
	DEX string
	// Bootstrap 是挂到 -Xbootclasspath 上的引导 dex，提供从内存加载 DEX 的入口。
	Bootstrap string
}

const (
	// bootstrapClass 必须在 app_process 自带 boot classpath 内，否则退化为
	// 文件式加载。tests/ci_verify.sh 校验它确实被打进 boot dex。
	bootstrapClass = "com.fanjv.netproxy.platform.Bootstrap"
	// dexLimit 限制下发体积，异常输入不能占满子进程内存。
	dexLimit = 32 << 20
)

func (client Client) Query(ctx context.Context, request Request) (jsontext.Value, error) {
	content, err := json.Marshal(request, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(client.Bootstrap); err != nil || info.Size() == 0 {
		// 缺引导 dex 时 app_process 无法进入内存加载路径。
		return nil, &contract.Error{Code: "android.unavailable", Message: "Android 平台查询失败"}
	}
	dex, err := os.ReadFile(client.DEX)
	if err != nil || len(dex) == 0 || len(dex) > dexLimit {
		return nil, &contract.Error{Code: "android.unavailable", Message: "Android 平台查询失败"}
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/system/bin/app_process",
		"-Xbootclasspath:"+client.Bootstrap, "/system/bin", bootstrapClass)
	// 帧格式：<dex 长度:u32 大端><dex 字节><请求 JSON>。
	command.Stdin = bytes.NewReader(Frame(dex, content))
	command.WaitDelay = time.Second
	output := limitedBuffer{limit: 8 << 20}
	command.Stdout = &output
	runErr := command.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return decodeResponse(output.Bytes(), runErr)
}

// Frame 组装引导类读取的 stdin 帧；引导类与测试共用同一实现，避免协议漂移。
func Frame(dex []byte, request []byte) []byte {
	frame := make([]byte, 4, 4+len(dex)+len(request))
	binary.BigEndian.PutUint32(frame, uint32(len(dex)))
	frame = append(frame, dex...)
	return append(frame, request...)
}

func decodeResponse(output []byte, runErr error) (jsontext.Value, error) {
	if runErr != nil && len(output) == 0 {
		return nil, &contract.Error{Code: "android.unavailable", Message: "Android 平台查询失败"}
	}
	var result struct {
		Schema  int            `json:"schema"`
		OK      bool           `json:"ok"`
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Data    jsontext.Value `json:"data"`
	}
	if err := json.Unmarshal(output, &result, json.RejectUnknownMembers(true)); err != nil || result.Schema != 1 || result.Code == "" {
		return nil, &contract.Error{Code: "android.invalid_output", Message: "Android 平台没有返回有效数据"}
	}
	if !result.OK || runErr != nil {
		// 辅助程序不能把输入凭据或系统异常文本回传为界面错误。
		return nil, &contract.Error{Code: "android.query_failed", Message: "无法读取 Android 平台信息"}
	}
	return result.Data, nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (buffer *limitedBuffer) Write(value []byte) (int, error) {
	if len(value) > buffer.limit-buffer.Len() {
		return 0, errors.New("平台响应超出大小限制")
	}
	return buffer.Buffer.Write(value)
}
