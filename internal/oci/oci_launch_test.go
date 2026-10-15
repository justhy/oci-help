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

// TestSleepRandomSecondUsesConfiguredInterval 锁定「尝试间隔」确实生效：
// 传入的 min/max 必须体现在 "Sleep N Second..." 日志上（min==max 时固定 N 秒）。
// 通过 Stopped 钩子让函数立即返回，避免测试真的等待。
func TestSleepRandomSecondUsesConfiguredInterval(t *testing.T) {
	cases := []struct {
		name     string
		min, max int32
		want     string
	}{
		{"固定 30 秒", 30, 30, "Sleep 30 Second..."},
		{"固定 60 秒", 60, 60, "Sleep 60 Second..."},
		{"固定 1 秒", 1, 1, "Sleep 1 Second..."},
		{"非法值退化为 1 秒", 0, 0, "Sleep 1 Second..."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			ac := &AccountCtx{
				Logger:  func(format string, a ...interface{}) { got = fmt.Sprintf(format, a...) },
				Stopped: func() bool { return true }, // 立即中止，不真正 sleep
			}
			if ac.sleepRandomSecond(c.min, c.max) {
				t.Fatal("Stopped() 为真时应返回 false")
			}
			if strings.TrimSpace(got) != c.want {
				t.Fatalf("sleepRandomSecond(%d,%d) 日志 = %q, want %q", c.min, c.max, got, c.want)
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

// TestRetryDelay 覆盖「重试间隔最小/最大」的随机取值与回退规则。
func TestRetryDelay(t *testing.T) {
	// 未配置（0/0）→ 回退到 fallback 区间
	for i := 0; i < 100; i++ {
		if got := RetryDelay(0, 0, 5, 9); got < 5 || got > 9 {
			t.Fatalf("未配置应落在 fallback [5,9], got %d", got)
		}
	}
	// 只给 min → 固定 min
	if got := RetryDelay(7, 0, 1, 1); got != 7 {
		t.Fatalf("只给 min 应固定为 7, got %d", got)
	}
	// 只给 max → 落在 [fallbackMin, max]
	for i := 0; i < 100; i++ {
		if got := RetryDelay(0, 12, 1, 1); got < 1 || got > 12 {
			t.Fatalf("只给 max 应落在 [1,12], got %d", got)
		}
	}
	// 正常区间：随机且不越界
	seen := map[int32]bool{}
	for i := 0; i < 300; i++ {
		got := RetryDelay(3, 8, 0, 0)
		if got < 3 || got > 8 {
			t.Fatalf("应落在 [3,8], got %d", got)
		}
		seen[got] = true
	}
	if len(seen) < 2 {
		t.Fatalf("区间内应产生随机值，实际只出现 %v", seen)
	}
	// 全部无效 → 1 秒
	if got := RetryDelay(0, 0, 0, 0); got != 1 {
		t.Fatalf("全部无效应为 1, got %d", got)
	}
}

// TestApplyBackoff 覆盖 429 指数退避：2 的幂放大并封顶。
func TestApplyBackoff(t *testing.T) {
	cases := []struct {
		base, streak, want int32
	}{
		{5, 0, 5},    // 非限流
		{5, 1, 5},    // 首次限流不放大
		{5, 2, 10},   // 2^1
		{5, 3, 20},   // 2^2
		{5, 4, 40},   // 2^3
		{10, 6, 180}, // 10<<5=320 → 封顶 180
		{0, 5, 0},    // 基础为 0 保持 0
	}
	for _, c := range cases {
		if got := applyBackoff(c.base, c.streak); got != c.want {
			t.Fatalf("applyBackoff(%d,%d)=%d, want %d", c.base, c.streak, got, c.want)
		}
	}
}

// TestIsRateLimitError 覆盖限流识别（429 / TooManyRequests）。
func TestIsRateLimitError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"429", fakeServiceError{429, "TooManyRequests", "slow down"}, true},
		{"code 为 TooManyRequests", fakeServiceError{400, "TooManyRequests", "x"}, true},
		{"包装后的 429 仍可识别", fmt.Errorf("调用失败: %w", fakeServiceError{429, "TooManyRequests", "slow down"}), true},
		{"文案含 Too many requests", errors.New("Too many requests for the user"), true},
		{"普通 500 不算限流", fakeServiceError{500, "InternalError", "x"}, false},
		{"网络错误不算限流", errors.New("dial tcp: connection refused"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isRateLimitError(c.err); got != c.want {
				t.Fatalf("isRateLimitError=%v, want %v", got, c.want)
			}
		})
	}
}
