package oci

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeServiceError 模拟 OCI 服务端错误，同时实现 common.ServiceError 与 error。
type fakeServiceError struct {
	status int
	code   string
	msg    string
}

func (e fakeServiceError) Error() string           { return e.msg }
func (e fakeServiceError) GetHTTPStatusCode() int  { return e.status }
func (e fakeServiceError) GetMessage() string      { return e.msg }
func (e fakeServiceError) GetCode() string         { return e.code }
func (e fakeServiceError) GetOpcRequestID() string { return "opc-test-request-id" }

// TestClassifyLaunchError 覆盖 H1 回归：网络类错误必须判为"可重试"且不得 panic，
// 只有真正的 OCI 服务错误才按状态码判定。
func TestClassifyLaunchError(t *testing.T) {
	netErr := errors.New("dial tcp 10.0.0.1:443: connect: connection refused")
	wrapped := fmt.Errorf("LaunchInstance 请求失败: %w", errors.New("EOF"))

	cases := []struct {
		name        string
		err         error
		wantSkip    bool
		wantMsgPart string
	}{
		{"nil 错误", nil, false, ""},

		// —— H1 回归：非服务错误（原先会走 nil 接口 → panic）——
		{"网络错误应可重试", netErr, false, "connection refused"},
		{"包装后的网络错误应可重试", wrapped, false, "EOF"},

		// —— 不可重试：4xx 参数/鉴权/前置条件类 ——
		{"400 不可重试", fakeServiceError{400, "InvalidParameter", "bad param"}, true, "bad param"},
		{"401 不可重试", fakeServiceError{401, "NotAuthenticated", "auth failed"}, true, "auth failed"},
		{"404 不可重试", fakeServiceError{404, "NotAuthorizedOrNotFound", "not found"}, true, "not found"},
		{"405 不可重试", fakeServiceError{405, "MethodNotAllowed", "nope"}, true, "nope"},
		{"409 Conflict 不可重试", fakeServiceError{409, "Conflict", "conflict"}, true, "conflict"},
		{"412 不可重试", fakeServiceError{412, "PreconditionFailed", "pre"}, true, "pre"},
		{"413 不可重试", fakeServiceError{413, "PayloadTooLarge", "big"}, true, "big"},
		{"422 不可重试", fakeServiceError{422, "LimitExceeded", "limit"}, true, "limit"},
		{"431 不可重试", fakeServiceError{431, "RequestHeaderFieldsTooLarge", "hdr"}, true, "hdr"},
		{"501 不可重试", fakeServiceError{501, "NotImplemented", "ni"}, true, "ni"},

		// —— 可重试：容量/限流/服务端临时错误 ——
		{"409 IncorrectState 可重试", fakeServiceError{409, "IncorrectState", "wrong state"}, false, "wrong state"},
		{"409 IncorrectState 大小写不敏感", fakeServiceError{409, "incorrectstate", "x"}, false, "x"},
		{"429 限流可重试", fakeServiceError{429, "TooManyRequests", "slow down"}, false, "slow down"},
		{"500 容量不足可重试", fakeServiceError{500, "OutOfHostCapacity", "no capacity"}, false, "no capacity"},
		{"503 可重试", fakeServiceError{503, "ServiceUnavailable", "unavailable"}, false, "unavailable"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			skip, msg := classifyLaunchError(c.err)
			if skip != c.wantSkip {
				t.Fatalf("skip=%v, want %v", skip, c.wantSkip)
			}
			if c.wantMsgPart != "" && !strings.Contains(msg, c.wantMsgPart) {
				t.Fatalf("msg=%q, 期望包含 %q", msg, c.wantMsgPart)
			}
		})
	}
}

// TestEachADIndex 覆盖 H2 回归：each 模式下目标域按 pos/each 均匀分布，
// 可重试失败时必须轮转到下一个域（原先 failTimes!=0 后永不切换 → retry=-1 死锁）。
func TestEachADIndex(t *testing.T) {
	cases := []struct {
		name                          string
		pos, each, failTimes, adCount int32
		want                          int32
	}{
		// 无失败：每个可用性域创建 each 个
		{"each=1 第0个→AD0", 0, 1, 0, 3, 0},
		{"each=1 第1个→AD1", 1, 1, 0, 3, 1},
		{"each=1 第2个→AD2", 2, 1, 0, 3, 2},
		{"each=2 第0个→AD0", 0, 2, 0, 3, 0},
		{"each=2 第1个→AD0", 1, 2, 0, 3, 0},
		{"each=2 第2个→AD1", 2, 2, 0, 3, 1},
		{"each=2 第4个→AD2", 4, 2, 0, 3, 2},
		{"each=2 第6个绕回→AD0", 6, 2, 0, 3, 0},

		// H2 回归：可重试失败 → 轮转到下一个域
		{"each=1 失败1次→AD1", 0, 1, 1, 3, 1},
		{"each=1 失败2次→AD2", 0, 1, 2, 3, 2},
		{"each=1 失败3次绕回→AD0", 0, 1, 3, 3, 0},
		{"each=2 失败1次→AD1", 0, 2, 1, 3, 1},
		{"each=2 失败5次→AD2", 0, 2, 5, 3, 2},

		// 防御：非法参数不 panic、不越界
		{"adCount=0 返回0", 5, 2, 0, 0, 0},
		{"each=0 返回0", 5, 0, 0, 3, 0},
		{"adCount<0 返回0", 5, 2, 0, -1, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := eachADIndex(c.pos, c.each, c.failTimes, c.adCount)
			if got != c.want {
				t.Fatalf("eachADIndex(%d,%d,%d,%d)=%d, want %d",
					c.pos, c.each, c.failTimes, c.adCount, got, c.want)
			}
			if c.adCount > 0 && (got < 0 || got >= c.adCount) {
				t.Fatalf("下标 %d 越界 [0,%d)", got, c.adCount)
			}
		})
	}
}
